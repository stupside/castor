package hls

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Resolve narrows the source to the rendition to read and builds the program around it.
func (Format) Resolve(ctx context.Context, env source.Env, s source.Subject) (source.Resolution, error) {
	stream := s.Stream
	origin, chosen, primaryRelaxed := inspect(ctx, env, stream, s.Origin, s.Chosen)
	read := stream.URL
	if chosen.URL != nil {
		read = chosen.URL
	}
	// What this document says about the rung it named, backed by what the caller already knew.
	chosen = backedBy(chosen, s.Chosen)
	audioURL := chosen.AudioURL

	inputs := []media.Input{{
		ID:                   media.PrimaryInputID,
		URL:                  read,
		Headers:              env.Client.Replay(read, stream.Headers),
		ContentType:          stream.ContentType,
		RequiresRelaxedInput: primaryRelaxed,
		Fetch:                media.Fetch{Segmented: origin.Segmented, Framing: origin.Framing, Live: origin.Live, Spliced: origin.Spliced},
	}}
	tracks := []media.TrackRef{
		{Input: media.PrimaryInputID, Kind: media.TrackVideo, Optional: true},
		{Input: media.PrimaryInputID, Kind: media.TrackAudio, Optional: true},
	}

	end := media.EndAtLongest
	if audioURL != nil {
		end = media.EndAtShortest
		audioHeaders := env.Client.Replay(audioURL, stream.Headers)
		audioFetch, audioRelaxed, err := inspectCompanion(ctx, env.Client, audioURL, audioHeaders)
		if err != nil {
			return source.Resolution{Origin: origin, Rendition: chosen}, err
		}
		inputs = append(inputs, media.Input{
			ID:                   media.AudioInputID,
			URL:                  audioURL,
			Headers:              audioHeaders,
			ContentType:          media.HLS,
			RequiresRelaxedInput: audioRelaxed,
			Fetch:                audioFetch,
		})
		// The rendition exists only to carry sound, so its sound is no longer optional.
		tracks[1].Input, tracks[1].Optional = media.AudioInputID, false
		slog.InfoContext(ctx, "source publishes audio separately; both renditions will be read",
			"video", read.String(), "audio", audioURL.String())
	}

	program, err := source.Narrowed(media.Program{Inputs: inputs, Tracks: tracks, ClockInput: media.PrimaryInputID, EndPolicy: end}, chosen, &stream)
	if err != nil {
		return source.Resolution{Origin: origin, Rendition: chosen}, fmt.Errorf("narrowing the HLS program to the chosen rendition: %w", err)
	}
	return source.Resolution{Program: program, Origin: origin, Rendition: chosen}, nil
}

// inspect narrows a source to the single rendition to read and returns what the source published; relaxed is also true whenever that stays unknown, which licenses no self-fetch.
func inspect(ctx context.Context, env source.Env, stream source.Stream, origin source.Origin, settled source.Rendition) (_ source.Origin, _ source.Rendition, relaxed bool) {
	doc, status, err := readPlaylist(ctx, env.Client, stream.URL, stream.Headers)
	if err != nil {
		slog.WarnContext(ctx, "HLS playlist resolution failed, using original", "error", err, "status", status)
		return origin, source.Rendition{}, true
	}
	if len(doc.variants) == 0 {
		// A master listing nothing but I-frame playlists reduces to no castable rendition.
		slog.WarnContext(ctx, "playlist offers no castable rendition, using original", "url", stream.URL.String())
		return origin, source.Rendition{}, true
	}
	origin.Renditions = ladder(doc, stream.Probe)
	chosen := env.Choose(ctx, origin, settled, byBitrate)

	// Segment facts are stated by the document that LISTS the segments (chosen variant's own playlist).
	segments := doc
	if doc.multivariant {
		segments, status, err = readPlaylist(ctx, env.Client, chosen.URL, stream.Headers)
		if err != nil {
			slog.WarnContext(ctx, "the chosen rendition's playlist could not be read; the source's own facts stay unknown",
				"error", err, "status", status, "url", chosen.URL.String())
			return origin, chosen, true
		}
		// A rung naming another master states nothing about segments.
		if segments.multivariant {
			slog.WarnContext(ctx, "the chosen rendition names another multivariant playlist; the source's own facts stay unknown",
				"url", chosen.URL.String())
			return origin, chosen, true
		}
	}
	origin.Framing = segments.framing
	origin.Protection = segments.protection
	origin.Spliced = segments.spliced
	// EXT-X-ENDLIST in the document that lists the segments is the proof of an end.
	origin.Live = segments.live
	if segments.duration > 0 {
		// The EXTINF sum wins: ffprobe reports no duration for most playlists.
		origin.Duration = segments.duration
	}
	return origin, chosen, segments.requiresRelaxedInput
}

// inspectCompanion reads the audio rendition's media playlist.
func inspectCompanion(ctx context.Context, client source.Client, audioURL *url.URL, headers http.Header) (media.Fetch, bool, error) {
	unknown := media.Fetch{Segmented: true}
	doc, status, err := readPlaylist(ctx, client, audioURL, headers)
	if err != nil {
		slog.WarnContext(ctx, "the companion audio playlist could not be read; its fetch characteristics stay unknown",
			"error", err, "status", status, "url", audioURL.String())
		return unknown, true, nil
	}
	if doc.multivariant {
		slog.WarnContext(ctx, "the companion audio URL names a multivariant playlist; its segment characteristics stay unknown",
			"url", audioURL.String())
		return unknown, true, nil
	}
	if doc.protection != "" {
		return unknown, false, fmt.Errorf("the companion audio is protected by DRM (%s), which castor cannot decrypt", doc.protection)
	}
	return media.Fetch{
		Segmented: true,
		Framing:   doc.framing,
		Live:      doc.live,
		Spliced:   doc.spliced,
	}, doc.requiresRelaxedInput, nil
}

// readPlaylist fetches one HLS document and reduces it to the facts it states.
func readPlaylist(ctx context.Context, client source.Client, u *url.URL, headers http.Header) (document, int, error) {
	body, from, status, err := client.Fetch(ctx, u, headers)
	if err != nil {
		return document{}, status, err
	}
	// A master's references resolve against where it came from, after any redirect.
	doc, err := parsePlaylist(body, u, from)
	return doc, status, err
}
