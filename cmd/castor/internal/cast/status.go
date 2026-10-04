package cast

import (
	"context"
	"log/slog"

	"google.golang.org/protobuf/proto"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// changed logs what moved between two statuses of a cast.
func changed(ctx context.Context, last, now *castorv1.CastStatus) {
	var streams, castable uint32
	var ranked bool
	switch previous := last.GetState().(type) {
	case *castorv1.CastStatus_Measuring:
		streams, castable = previous.Measuring.GetStreams(), previous.Measuring.GetCastable()
		ranked = previous.Measuring.Castable != nil
	case *castorv1.CastStatus_Casting:
		streams, castable, ranked = previous.Casting.GetStreams(), previous.Casting.GetCastable(), true
	}
	switch state := now.GetState().(type) {
	case *castorv1.CastStatus_Connecting:
		if _, same := last.GetState().(*castorv1.CastStatus_Connecting); !same {
			slog.InfoContext(ctx, "cast connecting the device")
		}
	case *castorv1.CastStatus_Extracting:
		if _, same := last.GetState().(*castorv1.CastStatus_Extracting); !same {
			slog.InfoContext(ctx, "cast finding streams")
		}
	case *castorv1.CastStatus_Measuring:
		if measured := state.Measuring; measured.GetStreams() != streams {
			slog.InfoContext(ctx, "cast measuring", "streams", measured.GetStreams())
		}
		if measured := state.Measuring; measured.Castable != nil && (!ranked || measured.GetCastable() != castable) {
			slog.InfoContext(ctx, "cast measured", "castable", measured.GetCastable())
		}
	case *castorv1.CastStatus_Casting:
		casting := state.Casting
		if casting.GetStreams() != streams {
			slog.InfoContext(ctx, "cast measuring", "streams", casting.GetStreams())
		}
		if !ranked || casting.GetCastable() != castable {
			slog.InfoContext(ctx, "cast measured", "castable", casting.GetCastable())
		}
		previous := last.GetCasting()
		if revision := casting.GetRevision(); revision != nil && !proto.Equal(revision, previous.GetRevision()) {
			slog.WarnContext(ctx, "cast revising", "action", revision.GetAction().String(), "why", revision.GetWhy())
		}
		if casting.GetAttempt() != previous.GetAttempt() {
			slog.InfoContext(ctx, "cast attempting", "try", casting.GetAttempt())
		}
	}
}
