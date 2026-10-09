// Package source loads Collector configs from files and Kubernetes.
package source

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/pratik-mahalle/otelkit/internal/model"
)

// CollectorGVR is the OpenTelemetry Operator's collector resource.
var CollectorGVR = schema.GroupVersionResource{Group: "opentelemetry.io", Version: "v1beta1", Resource: "opentelemetrycollectors"}

// Target is one loaded Collector config.
type Target struct {
	Name     string
	Source   string // the source argument it was loaded from
	Root     *yaml.Node
	Deployed Deployed
}

// Deployed locates a running config: a file or a Kubernetes object.
type Deployed struct {
	File string  `yaml:"file,omitempty"`
	K8s  *K8sRef `yaml:"k8s,omitempty"`
}

// K8sRef points at a ConfigMap key or an OpenTelemetryCollector resource.
type K8sRef struct {
	Context   string `yaml:"context"`
	Namespace string `yaml:"namespace"`
	ConfigMap string `yaml:"configmap,omitempty"`
	Key       string `yaml:"key,omitempty"`
	Otelcol   string `yaml:"otelcol,omitempty"`
}

// Clients builds Kubernetes clients for a kubeconfig context.
type Clients func(context string) (kubernetes.Interface, dynamic.Interface, error)

// DefaultClients uses the user's kubeconfig.
func DefaultClients(kubeContext string) (kubernetes.Interface, dynamic.Interface, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		return nil, nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return cs, dc, nil
}

// Loader loads sources. Clients may be nil when no k8s:// sources are used.
type Loader struct {
	Clients Clients
	Aliases map[string]string // source argument → target name, overriding the default
}

// Load resolves file paths, directories, globs and k8s:// URLs into targets.
// A failing source is reported in errs and does not stop the others.
func (l Loader) Load(ctx context.Context, args []string) ([]Target, []error) {
	var targets []Target
	var errs []error
	var paths, pathArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "k8s://") {
			ref, err := ParseK8s(a)
			if err == nil {
				var root *yaml.Node
				if root, err = l.loadK8s(ctx, ref); err == nil {
					targets = append(targets, Target{Name: ref.name(), Source: a, Root: root, Deployed: Deployed{K8s: &ref}})
					continue
				}
			}
			errs = append(errs, fmt.Errorf("%s: %w", a, err))
			continue
		}
		ps, err := expand(a)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, p := range ps {
			if abs, err := filepath.Abs(p); err == nil && slices.ContainsFunc(paths, func(q string) bool {
				qa, _ := filepath.Abs(q)
				return qa == abs
			}) {
				continue // the same file given twice, e.g. via a directory and a glob
			}
			paths, pathArgs = append(paths, p), append(pathArgs, a)
		}
	}
	names := fileNames(paths)
	for i, p := range paths {
		root, err := readFile(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		targets = append(targets, Target{Name: names[i], Source: pathArgs[i], Root: root, Deployed: Deployed{File: abs}})
	}
	qualifyK8s(targets)
	var named []Target
	for _, t := range targets {
		if alias, ok := l.Aliases[t.Source]; ok {
			if alias == "" || strings.ContainsAny(alias, `/\`) {
				errs = append(errs, fmt.Errorf("--name %q for %s: a name cannot be empty or contain a slash", alias, t.Source))
				continue
			}
			t.Name = alias
		}
		named = append(named, t)
	}
	return unique(named, errs)
}

// qualifyK8s prefixes the context to Kubernetes target names that would otherwise collide.
func qualifyK8s(targets []Target) {
	count := map[string]int{}
	for _, t := range targets {
		if t.Deployed.K8s != nil {
			count[t.Name]++
		}
	}
	for i, t := range targets {
		if t.Deployed.K8s != nil && count[t.Name] > 1 {
			targets[i].Name = t.Deployed.K8s.contextName() + "-" + t.Name
		}
	}
}

// unique drops targets whose name is already taken, reporting each, so no config is silently merged into another.
func unique(targets []Target, errs []error) ([]Target, []error) {
	first := map[string]Target{}
	var out []Target
	for _, t := range targets {
		if f, ok := first[t.Name]; ok {
			errs = append(errs, fmt.Errorf("%s: target name %q is already used by %s; rename one of them", t.Source, t.Name, f.Source))
			continue
		}
		first[t.Name] = t
		out = append(out, t)
	}
	return out, errs
}

// FromDeployed loads the config a fleet target says is deployed. Relative files resolve against dir.
func (l Loader) FromDeployed(ctx context.Context, dir string, d Deployed) (*yaml.Node, error) {
	if d.K8s != nil {
		return l.loadK8s(ctx, *d.K8s)
	}
	if d.File == "" {
		return nil, fmt.Errorf("deployed has neither file nor k8s")
	}
	p := d.File
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return readFile(p)
}

func expand(arg string) ([]string, error) {
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		var out []string
		for _, pat := range []string{"*.yaml", "*.yml"} {
			m, _ := filepath.Glob(filepath.Join(arg, pat))
			out = append(out, m...)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s: no .yaml files", arg)
		}
		slices.Sort(out)
		return out, nil
	}
	if !strings.ContainsAny(arg, "*?[") {
		return []string{arg}, nil
	}
	m, err := filepath.Glob(arg)
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("no files match %q", arg)
	}
	return m, nil
}

// fileNames names targets by file base name; colliding names use the path with / replaced by -.
func fileNames(paths []string) []string {
	base := func(p string) string { return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)) }
	count := map[string]int{}
	for _, p := range paths {
		count[base(p)]++
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		if count[base(p)] == 1 {
			out[i] = base(p)
			continue
		}
		rel := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(p)), filepath.Ext(p))
		out[i] = strings.ReplaceAll(strings.TrimPrefix(rel, "/"), "/", "-")
	}
	return out
}

func readFile(p string) (*yaml.Node, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	root, err := model.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return root, nil
}

// ParseK8s parses k8s://<context>/<namespace>/configmap/<name>[#key] or
// k8s://<context>/<namespace>/otelcol/<name>. Parsed from the right: contexts may contain "/".
func ParseK8s(s string) (K8sRef, error) {
	rest, key, _ := strings.Cut(strings.TrimPrefix(s, "k8s://"), "#")
	parts := strings.Split(rest, "/")
	n := len(parts)
	if n < 4 {
		return K8sRef{}, fmt.Errorf("%s: want k8s://<context>/<namespace>/configmap|otelcol/<name>", s)
	}
	ref := K8sRef{Context: strings.Join(parts[:n-3], "/"), Namespace: parts[n-3]}
	switch parts[n-2] {
	case "configmap":
		ref.ConfigMap, ref.Key = parts[n-1], key
	case "otelcol":
		ref.Otelcol = parts[n-1]
	default:
		return K8sRef{}, fmt.Errorf("%s: kind must be configmap or otelcol, got %q", s, parts[n-2])
	}
	return ref, nil
}

// name is <namespace>-<object>; qualifyK8s adds the context when two targets share it.
func (r K8sRef) name() string {
	obj := r.ConfigMap
	if obj == "" {
		obj = r.Otelcol
	}
	return r.Namespace + "-" + obj
}

// contextName is the last part of the context, e.g. prod-eu for an EKS ARN ending in cluster/prod-eu.
func (r K8sRef) contextName() string {
	ctx := r.Context
	if i := strings.LastIndexAny(ctx, "/:"); i >= 0 {
		ctx = ctx[i+1:]
	}
	return ctx
}

func (l Loader) loadK8s(ctx context.Context, r K8sRef) (*yaml.Node, error) {
	if l.Clients == nil {
		return nil, fmt.Errorf("kubernetes access not configured")
	}
	cs, dc, err := l.Clients(r.Context)
	if err != nil {
		return nil, err
	}
	if r.ConfigMap != "" {
		cm, err := cs.CoreV1().ConfigMaps(r.Namespace).Get(ctx, r.ConfigMap, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		key := r.Key
		if key == "" {
			key = defaultKey(cm.Data)
		}
		data, ok := cm.Data[key]
		if !ok {
			return nil, fmt.Errorf("configmap %s/%s has no key %q (keys: %s)", r.Namespace, r.ConfigMap, key,
				strings.Join(slices.Sorted(maps.Keys(cm.Data)), ", "))
		}
		return model.Parse([]byte(data))
	}
	obj, err := dc.Resource(CollectorGVR).Namespace(r.Namespace).Get(ctx, r.Otelcol, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	cfg, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "config")
	if err != nil || !found {
		return nil, fmt.Errorf("%s/%s has no spec.config", r.Namespace, r.Otelcol)
	}
	if s, ok := cfg.(string); ok {
		return model.Parse([]byte(s))
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return model.Parse(data)
}

// defaultKey picks config.yaml, else relay (the Helm chart's key), else the only key.
func defaultKey(data map[string]string) string {
	for _, k := range []string{"config.yaml", "relay"} {
		if _, ok := data[k]; ok {
			return k
		}
	}
	if len(data) == 1 {
		for k := range data {
			return k
		}
	}
	return "config.yaml"
}
