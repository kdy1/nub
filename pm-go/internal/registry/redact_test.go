package registry

import "testing"

func TestURLDiagnosticRedaction(t *testing.T) {
	for raw, want := range map[string]string{
		"https://user:password@host/pkg?token=secret&v=1#frag":       "https://***@host/pkg?token=***&v=1#frag",
		"//user:pw@[::1]:8443/pkg?Auth=secret":                       "//***@[::1]:8443/pkg?Auth=***",
		"https://host/pkg@1?apikey=x&api_key=y&access_token=z&token": "https://host/pkg@1?apikey=***&api_key=***&access_token=***&token",
		"https://host/pkg?foo=a%20b&bar=2#part":                      "https://host/pkg?foo=a%20b&bar=2#part",
		"not-a-url?token=x":                                          "not-a-url?token=***",
	} {
		if got := RedactURL(raw); got != want {
			t.Fatal(got, want)
		}
	}
}
