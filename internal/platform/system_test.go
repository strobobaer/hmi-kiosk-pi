package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const (
	policy015 = `{"URLBlocklist": ["*"], "URLAllowlist": ["http://127.0.0.1:8080", "http://localhost:8080"]}`
	policy016 = `{"URLBlocklist": ["*"], "URLAllowlist": ["http://127.0.0.1:8080", "http://localhost:8080", "http://127.0.0.1:8081", "http://localhost:8081", "http://.grafana:3000"]}`
)

func TestChromiumAllows(t *testing.T) {
	const origin = "http://127.0.0.1:8081"
	for _, tc := range []struct {
		name   string
		policy *string
		want   bool
	}{
		{"keine Richtlinie sperrt nichts", nil, true},
		{"0.1.5 sperrt die Admin-Seite", ptr(policy015), false},
		{"0.1.6 erlaubt sie", ptr(policy016), true},
		{"ohne Sperrliste alles offen", ptr(`{"URLAllowlist": []}`), true},
		{"Eintrag ohne Schema", ptr(`{"URLBlocklist": ["*"], "URLAllowlist": ["127.0.0.1:8081/"]}`), true},
		{"anderer Port reicht nicht", ptr(`{"URLBlocklist": ["*"], "URLAllowlist": ["http://127.0.0.1:8082"]}`), false},
		{"kaputte Richtlinie gilt als Sperre", ptr(`{"URLBlocklist": [`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			if tc.policy != nil {
				files[ChromiumPolicyFile] = *tc.policy
			}
			if got := ChromiumAllows(writeRoot(t, files), origin); got != tc.want {
				t.Fatalf("ChromiumAllows = %v, erwartet %v", got, tc.want)
			}
		})
	}
}

func TestInstalledSystemLevel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  int
	}{
		{"Kennzeichen", map[string]string{SystemLevelFile: "3\n", ChromiumPolicyFile: policy015}, 3},
		{"0.1.6 ohne Kennzeichen", map[string]string{ChromiumPolicyFile: policy016}, 1},
		{"0.1.5 ohne Kennzeichen", map[string]string{ChromiumPolicyFile: policy015}, 0},
		{"gar nichts", map[string]string{}, 0},
		{"ungültiges Kennzeichen", map[string]string{SystemLevelFile: "zwei", ChromiumPolicyFile: policy016}, 0},
		{"negatives Kennzeichen", map[string]string{SystemLevelFile: "-1"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := InstalledSystemLevel(writeRoot(t, tc.files)); got != tc.want {
				t.Fatalf("Stand %d, erwartet %d", got, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }
