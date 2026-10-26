package dlna

import (
	"fmt"
	"net/http"
)

// streamHeaders holds the served stream to what DLNA devices require of the response.
func streamHeaders(declared, features string, h http.Header) []string {
	var problems []string
	if got := h.Get("transferMode.dlna.org"); got != "Streaming" {
		problems = append(problems, fmt.Sprintf("transferMode.dlna.org = %q, want Streaming", got))
	}
	// The DIDL states the features; a response may leave them out, never contradict them.
	if got := h.Get("contentFeatures.dlna.org"); got != "" && got != features {
		problems = append(problems, fmt.Sprintf("contentFeatures.dlna.org = %q, want DIDL features %q", got, features))
	}
	if got := h.Get("Content-Type"); got != declared {
		problems = append(problems, fmt.Sprintf("served Content-Type %q, but the DIDL declared %q", got, declared))
	}
	return problems
}
