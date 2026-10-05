package roku

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// ensureChannel guarantees Castor's own dev channel occupies the sideload slot.
func (s *session) ensureChannel(ctx context.Context, cfg Config) error {
	apps, err := s.queryApps(ctx)
	if err != nil {
		return fmt.Errorf("querying roku apps: %w", err)
	}
	for _, a := range apps {
		if a.ID == devAppID {
			if strings.TrimSpace(a.Title) == channelTitle {
				return nil
			}
			if cfg.Password == "" {
				return errors.New("a different sideloaded channel occupies the Roku dev slot; set devices.roku.password so Castor can replace it")
			}
			break
		}
	}
	if cfg.Password == "" {
		return errors.New("roku channel not installed and no developer password set: enable Developer Mode on the Roku, set a web-server password, and put it in devices.roku.password")
	}
	slog.InfoContext(ctx, "sideloading roku channel", "host", s.ecp.Hostname())
	return s.sideloadChannel(ctx, cfg.Password)
}

type app struct {
	ID    string `xml:"id,attr"`
	Title string `xml:",chardata"`
}

func (s *session) queryApps(ctx context.Context) ([]app, error) {
	body, err := get(ctx, s.hc, s.ecp.JoinPath("query", "apps"), 1<<20)
	if err != nil {
		return nil, err
	}
	var list struct {
		XMLName xml.Name `xml:"apps"`
		Apps    []app    `xml:"app"`
	}
	if err := xml.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decoding roku apps: %w", err)
	}
	return list.Apps, nil
}

func appInstalled(apps []app, id string) bool {
	for _, a := range apps {
		if a.ID == id {
			return true
		}
	}
	return false
}
