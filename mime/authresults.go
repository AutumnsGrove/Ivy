package mime

import (
	"strings"
	"unicode"
)

// authMethods are the verdicts the trust signal uses (ARCHITECTURE.md 5).
var authMethods = map[string]bool{"spf": true, "dkim": true, "dmarc": true}

// ParseAuthResults reads SPF, DKIM and DMARC verdicts from the topmost
// Authentication-Results header whose authserv-id is in trusted, and ignores
// every other header. The zero trusted list trusts nothing, so a caller that
// does not configure trust gets no verdict rather than a forged one.
//
// RFC 8601 sections 2.5 and 4.1: a header is trustworthy only when its
// authserv-id names the ADMD that added it, and a consumer must be configured
// before it interprets any of them. Purelymail adds no SPF/DKIM/DMARC verdicts
// (spike S1), so without this check a sender-supplied header is the only one and
// its "pass" switches off the spoofed-sender discount (N9 in papercuts.md).
//
// Only the first trusted header counts, not the first verdict per method. A
// later header could otherwise add a method the trusted one deliberately omitted,
// which is the same forgery by another route. Message order is newest first.
func ParseAuthResults(values []string, trusted []string) AuthResults {
	out := AuthResults{Raw: append([]string(nil), values...)}
	allow := trustedAuthservIDs(trusted)
	if len(allow) == 0 {
		return out
	}
	for _, value := range values {
		id := authservID(value)
		if id == "" || !allow[id] {
			continue
		}
		out.AuthservID = id
		setVerdicts(&out, parseAuthHeader(value))
		return out
	}
	return out
}

// trustedAuthservIDs normalizes the configured ids into a set. Blanks drop out,
// and ids that differ only by case or a trailing dot collapse, so the operator
// does not have to match the exact spelling a server happens to use.
func trustedAuthservIDs(trusted []string) map[string]bool {
	if len(trusted) == 0 {
		return nil
	}
	out := make(map[string]bool, len(trusted))
	for _, id := range trusted {
		if id = normalizeAuthservID(id); id != "" {
			out[id] = true
		}
	}
	return out
}

// authservID returns the authentication service identifier of one header value:
// the token before the first ";" (RFC 8601 2.5), normalized. A header with no
// identifier is never trusted.
func authservID(value string) string {
	if i := strings.IndexByte(value, ';'); i >= 0 {
		value = value[:i]
	}
	return normalizeAuthservID(value)
}

// normalizeAuthservID lower-cases and trims one identifier and drops a trailing
// dot, so "MX.Example.NET." and "mx.example.net" are the same service.
func normalizeAuthservID(id string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(id)), ".")
}

// setVerdicts copies the methods of one parsed header onto the result, leaving a
// field empty when the trusted header did not carry that method.
func setVerdicts(out *AuthResults, methods map[string]string) {
	for method, verdict := range methods {
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

// parseAuthHeader reads one "authserv-id; method=result [props]" value. Verdict
// properties (reason, header.d, smtp.mailfrom, ...) are ignored, and only the
// first token after "=" is the result. Within one header an overall DKIM pass
// wins over a per-signature fail, because a domain that signs at all is not the
// spoofing case the trust signal is looking for.
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
