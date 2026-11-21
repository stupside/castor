// Package ssdp searches the LAN for devices that answer an SSDP M-SEARCH.
package ssdp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/huin/goupnp/httpu"
	upnp "github.com/huin/goupnp/ssdp"

	"github.com/stupside/castor/services/apiserver/internal/device/lan"
)

// sends repeats each M-SEARCH, as UDP may drop one.
const sends = 3

// Search sends an M-SEARCH for target from every LAN address for at most most, leaving a third of ctx's time to read the answers.
func Search(ctx context.Context, target string, most time.Duration) ([]*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, window(ctx, most))
	defer cancel()
	interfaces, err := lan.Interfaces()
	if err != nil {
		return nil, err
	}
	var addresses []string
	for _, iface := range interfaces {
		for _, held := range iface.Addresses {
			if address := held.Addr(); address.Is4() {
				addresses = append(addresses, address.String())
			}
		}
	}
	return searchFromEach(ctx, addresses, func(ctx context.Context, address string) ([]*http.Response, error) {
		client, err := httpu.NewHTTPUClientAddr(address)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return upnp.RawSearch(ctx, client, target, sends)
	})
}

// window is the share of ctx's remaining time a search of at most most may use.
func window(ctx context.Context, most time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return min(most, time.Until(deadline)*2/3)
	}
	return most
}

// searchFromEach searches from every address together, so one that cannot send loses only its own answers.
func searchFromEach(ctx context.Context, addresses []string, search func(context.Context, string) ([]*http.Response, error)) ([]*http.Response, error) {
	answers := make([][]*http.Response, len(addresses))
	failures := make([]error, len(addresses))
	var wg sync.WaitGroup
	for i, address := range addresses {
		wg.Go(func() {
			answers[i], failures[i] = search(ctx, address)
			if failures[i] != nil {
				slog.DebugContext(ctx, "ssdp search", "from", address, "error", failures[i])
			}
		})
	}
	wg.Wait()
	if len(addresses) > 0 && !slices.Contains(failures, nil) {
		return nil, fmt.Errorf("searching from every address failed: %w", errors.Join(failures...))
	}
	return onePerDevice(slices.Concat(answers...)), nil
}

// onePerDevice drops the repeats of a device that answered on more than one address.
func onePerDevice(responses []*http.Response) []*http.Response {
	seen := map[string]bool{}
	return slices.DeleteFunc(responses, func(response *http.Response) bool {
		key := cmp.Or(response.Header.Get("USN"), response.Header.Get("Location"))
		repeated := seen[key]
		seen[key] = true
		return repeated
	})
}
