package cast

import (
	"context"
	"fmt"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
)

// DryRun prints the streams source would play, best first, instead of casting it.
func DryRun(ctx context.Context, casts castorv1connect.CastServiceClient, source *castorv1.Source) error {
	resolved, err := casts.Resolve(ctx, &castorv1.ResolveRequest{Source: source})
	if err != nil {
		return err
	}
	for _, s := range resolved.GetRanked() {
		fmt.Println(dryRunRow(s))
	}
	return nil
}

// dryRunRow formats a stream as bandwidth and URL, preserving unknown bandwidth.
func dryRunRow(s *castorv1.RankedStream) string {
	bitrate := "unknown"
	if s.Bitrate != nil {
		bitrate = fmt.Sprint(s.GetBitrate())
	}
	row := fmt.Sprintf("%s\t%s", bitrate, s.GetUrl())
	if s.GetLastResort() {
		row += "\tlast resort"
	}
	return row
}
