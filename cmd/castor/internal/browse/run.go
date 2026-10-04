// Package browse asks the operator which device to cast to and which title to cast, browsing TMDB for it.
package browse

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/stupside/castor/cmd/castor/internal/browse/picker"
	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
	"github.com/stupside/castor/cmd/castor/internal/cast"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
)

// Catalog is the title lookups the browser makes: TMDB in use, a test seam so screens render from a fixed shelf.
type Catalog interface {
	Search(ctx context.Context, query string) ([]tmdb.SearchResult, error)
	Trending(ctx context.Context) ([]tmdb.SearchResult, error)
	Discover(ctx context.Context, p tmdb.DiscoverParams) (tmdb.Page, error)
	Genres(ctx context.Context) (tmdb.GenreCatalog, error)
	Details(ctx context.Context, media tmdb.Media, id int) (*tmdb.Details, error)
	TV(ctx context.Context, id int) (*tmdb.TVDetails, error)
	Season(ctx context.Context, tvID, seasonNumber int) (*tmdb.SeasonDetails, error)
	Poster(ctx context.Context, posterPath string) (io.ReadCloser, error)
}

// Run asks which device to cast to, unless d pins one, then which title; chosen is false when the operator leaves without both.
func Run(ctx context.Context, devices castorv1connect.DeviceServiceClient, catalog Catalog, d cast.Device) (to *castorv1.Target, sel Selection, chosen bool, err error) {
	err = held(func() error {
		var name, typ string
		var err error
		if to, name, typ, err = pickDevice(ctx, devices, d); err != nil || to == nil {
			return err
		}
		if sel, chosen, err = pickTitle(ctx, catalog, strings.ToUpper(typ)+"  "+name); err != nil {
			return fmt.Errorf("browse: %w", err)
		}
		return nil
	})
	return to, sel, chosen, err
}

// pickDevice is the device d pins, or the one the operator picks; nil when they leave without one.
func pickDevice(ctx context.Context, devices castorv1connect.DeviceServiceClient, d cast.Device) (to *castorv1.Target, name, typ string, err error) {
	if d.Host != "" {
		to, err = cast.Target(ctx, devices, d)
		return to, cmp.Or(d.Name, d.Host), d.Type, err
	}
	picked, ok, err := picker.Device(ctx, listDevices(devices), d.Name)
	if err != nil {
		return nil, "", "", fmt.Errorf("picking device: %w", err)
	}
	if !ok {
		return nil, "", "", nil
	}
	return &castorv1.Target{Target: &castorv1.Target_DeviceId{DeviceId: picked.GetId()}}, picked.GetName(), cast.DeviceTypeName(picked.GetType()), nil
}

// listDevices sweeps for the devices the API server finds, warning when it cannot.
func listDevices(devices castorv1connect.DeviceServiceClient) picker.Discover {
	return func(ctx context.Context) []*castorv1.Device {
		listed, err := devices.ListDevices(ctx, &castorv1.ListDevicesRequest{})
		if err != nil {
			slog.WarnContext(ctx, "listing devices", "error", err)
		}
		return listed.GetDevices()
	}
}

// Kind is what a Selection casts: a movie, or one episode of a show.
type Kind int

const (
	KindMovie Kind = iota
	KindEpisode
)

// Selection is the title the operator chose to cast.
type Selection struct {
	Kind    Kind
	TMDBID  string
	Title   string
	Season  uint
	Episode uint
}

// pickTitle runs the browser until a title is chosen, or the operator leaves without one.
func pickTitle(ctx context.Context, client Catalog, badge string) (Selection, bool, error) {
	final, err := tea.NewProgram(newModel(ctx, client, badge), tea.WithContext(ctx)).Run()
	if err != nil {
		return Selection{}, false, err
	}
	if fm, ok := final.(model); ok && fm.sel != nil {
		return *fm.sel, true, nil
	}
	return Selection{}, false, nil
}
