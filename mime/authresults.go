package mime

import (
	"strings"
	"unicode"
)

// authMethods are the verdicts the trust signal uses (ARCHITECTURE.md 5).
var authMethods = map[string]bool{"spf": true, "dkim": true, "dmarc": true}

// ParseAuthResults reads SPF, DKIM and DMARC verdicts from raw
// Authentication-Results header values in message order, which is newest
// header first. Each method takes its verdict from the first header that
// carries it, so a forged sender header below the receiver's cannot override
// it; within one header an overall DKIM pass wins over a per-signature fail.
func ParseAuthResults(values []string) AuthResults {
	out := AuthResults{Raw: append([]string(nil), values...)}
	seen := map[string]bool{}
	for _, value := range values {
		for method, verdict := range parseAuthHeader(value) {
			if seen[method] {
				continue
			}
			seen[method] = true
			switch method {
			case "spf":
				out.SPF = verdict
			case "dkim":
				out.DKIM = verdict
			case "dmarc":
				out.DMARC = verdict
			}
		}
	}
	return out
}

// parseAuthHeader reads one "authserv-id; method=result [props]" value. Verdict
// properties (reason, header.d, smtp.mailfrom, ...) are ignored, and only the
// first token after "=" is the result.
func parseAuthHeader(value string) map[string]string {
	out := map[string]string{}
	for _, segment := range strings.Split(value, ";") {
		segment = strings.TrimSpace(segment)
		eq := strings.IndexByte(segment, '=')
		if eq < 0 {
			continue
		}
		method := strings.ToLower(strings.TrimSpace(segment[:eq]))
		if !authMethods[method] {
			continue
		}
		verdict := strings.ToLower(strings.TrimSpace(segment[eq+1:]))
		if i := strings.IndexFunc(verdict, unicode.IsSpace); i >= 0 {
			verdict = verdict[:i]
		}
		if verdict == "" {
			continue
		}
		if prev, ok := out[method]; ok {
			if prev == "pass" || verdict != "pass" {
				continue
			}
		}
		out[method] = verdict
	}
	return out
}
