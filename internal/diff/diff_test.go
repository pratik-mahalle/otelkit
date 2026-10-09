package diff

import (
	"testing"

	"github.com/pratik-mahalle/otelkit/internal/model"
)

func flat(t *testing.T, in string) Flat {
	t.Helper()
	root, err := model.Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return Flatten(root)
}

func TestFlattenLeavesAndPresence(t *testing.T) {
	f := flat(t, "processors:\n  batch:\n    timeout: 5s\n")
	if f["processors.batch"].Text != Present {
		t.Errorf("missing presence entry: %v", f)
	}
	if f["processors.batch.timeout"].Text != "5s" {
		t.Errorf("timeout = %q", f["processors.batch.timeout"].Text)
	}
}

func TestFlattenNullAndEmptyMapAreEqual(t *testing.T) {
	a := flat(t, "processors:\n  batch:\n")
	b := flat(t, "processors:\n  batch: {}\n")
	if ch := Compare(a, b); len(ch) != 0 {
		t.Errorf("batch: and batch: {} must be equal, got %+v", ch)
	}
}

func TestFlattenSetVsOrderedLists(t *testing.T) {
	a := flat(t, "service:\n  pipelines:\n    traces:\n      receivers: [a, b]\n      processors: [x, y]\n")
	b := flat(t, "service:\n  pipelines:\n    traces:\n      receivers: [b, a]\n      processors: [y, x]\n")
	ch := Compare(a, b)
	if len(ch) != 1 || ch[0].Path != "service.pipelines.traces.processors" {
		t.Errorf("want only processor order to differ, got %+v", ch)
	}
}

func TestFlattenListOfMapsByIndex(t *testing.T) {
	f := flat(t, "processors:\n  resource:\n    attributes:\n      - key: cluster\n        value: a\n")
	if f["processors.resource.attributes.0.value"].Text != "a" {
		t.Errorf("got %v", f)
	}
}

func TestFlattenQuotesDottedKeys(t *testing.T) {
	f := flat(t, "exporters:\n  otlp:\n    headers:\n      x.org.id: \"1\"\n      x: \"2\"\n")
	p := `exporters.otlp.headers."x.org.id"`
	if f[p].Text != "1" {
		t.Fatalf("missing %s in %v", p, f)
	}
	if got := Split(p); len(got) != 4 || got[3] != "x.org.id" {
		t.Errorf("Split = %q", got)
	}
	if !Match("*.headers.*", p) {
		t.Error("headers glob must match a dotted header key")
	}
}

func TestCompare(t *testing.T) {
	a := flat(t, "a: 1\nb: 2\n")
	b := flat(t, "a: 1\nb: 3\nc: 4\n")
	ch := Compare(a, b)
	if len(ch) != 2 || ch[0].Path != "b" || ch[0].From.Text != "2" || ch[0].To.Text != "3" || ch[1].Path != "c" || ch[1].From != nil {
		t.Errorf("got %+v", ch)
	}
}

func TestMask(t *testing.T) {
	cases := []struct{ path, in, want string }{
		{"exporters.otlp.headers.authorization", "Bearer abc", "****"},
		{"exporters.otlp.headers.authorization", "${env:TOKEN}", "${env:TOKEN}"},
		{"extensions.basicauth.client_auth.password", "hunter2", "****"},
		{"processors.batch.timeout", "5s", "5s"},
		{"exporters.otlp", Present, Present},
	}
	for _, c := range cases {
		if got := Mask(c.path, c.in); got != c.want {
			t.Errorf("Mask(%s, %s) = %s, want %s", c.path, c.in, got, c.want)
		}
	}
}

func TestMatch(t *testing.T) {
	if !Match("*.endpoint", "exporters.otlp.endpoint") || !Match("*.tls.*_file", "exporters.otlp.tls.ca_file") {
		t.Error("expected matches")
	}
	if Match("*.endpoint", "exporters.otlp.endpoints") {
		t.Error("glob must anchor at the end")
	}
}

func TestMaskPlaceholderMixedWithLiteral(t *testing.T) {
	if got := Mask("exporters.otlp.headers.authorization", "Bearer sk-live-123 ${env:X}"); got != "****" {
		t.Errorf("literal next to a placeholder must be masked, got %q", got)
	}
}

func TestMaskPlaceholderWithDefault(t *testing.T) {
	if got := Mask("exporters.otlp.headers.authorization", "${env:TOKEN:-sk-live-123}"); got != "****" {
		t.Errorf("a placeholder's default value can hold a secret, got %q", got)
	}
}

func TestFlattenDeepNullDiffersFromAbsent(t *testing.T) {
	a := flat(t, "receivers:\n  otlp:\n    protocols:\n      grpc:\n      http:\n")
	b := flat(t, "receivers:\n  otlp:\n    protocols:\n      grpc:\n")
	if ch := Compare(a, b); len(ch) != 1 || ch[0].Path != "receivers.otlp.protocols.http" {
		t.Errorf("a null key must still count as present, got %+v", ch)
	}
	if ch := Compare(b, flat(t, "receivers:\n  otlp:\n    protocols:\n      grpc: {}\n")); len(ch) != 0 {
		t.Errorf("grpc: and grpc: {} must be equal, got %+v", ch)
	}
}

func TestMaskCommonSecretShapes(t *testing.T) {
	cases := [][2]string{
		{"extensions.basicauth/server.htpasswd.inline", "admin:SuperSecretPw"},
		{"exporters.azuremonitor.connection_string", "InstrumentationKey=abc"},
		{"exporters.sentry.dsn", "https://key@sentry.io/1"},
		{"receivers.sqlquery.datasource", "host=db password=x"},
		{"exporters.otlphttp.endpoint", "https://user:pass@collector:4318"},
	}
	for _, c := range cases {
		if got := Mask(c[0], c[1]); got != "****" {
			t.Errorf("Mask(%s) = %q, want ****", c[0], got)
		}
	}
}

func TestMaskKeepsAttributeNames(t *testing.T) {
	if got := Mask("processors.attributes.actions.0.key", "http.method"); got != "http.method" {
		t.Errorf("an attribute's key is its name, not a secret: %q", got)
	}
	if got := Mask("processors.resource.attributes.2.key", "k8s.pod.name"); got != "k8s.pod.name" {
		t.Errorf("resource attribute name masked: %q", got)
	}
	if got := Mask("exporters.otlp.headers.api-key", "abc"); got != "****" {
		t.Errorf("a header named api-key is still a secret: %q", got)
	}
}
