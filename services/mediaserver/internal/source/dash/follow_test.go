package dash

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

const mpdOpen = `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" `

// at is the presentation clock this many seconds after availabilityStartTime.
func at(seconds int) time.Time { return time.Date(2026, 1, 1, 0, 0, seconds, 0, time.UTC) }

// window is what castor republishes for representation id of a presentation served at /live/manifest.mpd.
func window(t *testing.T, docs map[string]string, kind media.TrackKind, id string, now time.Time) timeline.Window {
	t.Helper()
	in := media.Input{ID: media.PrimaryInputID, URL: sourcetest.URL(t, "https://cdn.example/live/manifest.mpd"), Representation: id, ContentType: media.DASH}
	var w timeline.Window
	// The bubble's clock is the presentation's: it starts in 2000 and is moved on to now.
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(time.Until(now))
		follow := Format{}.Timeline(&sourcetest.Documents{ByPath: docs}, in, kind)
		var err error
		if w, err = follow.Window(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
	return w
}

func uris(w timeline.Window) []string {
	out := make([]string, len(w.Segments))
	for i, s := range w.Segments {
		out[i] = s.URI
	}
	return out
}

func same(t *testing.T, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("segments = %v\nwant       %v", got, want)
	}
}

func TestANumberedTimelineRunsEachRepeatToTheNextEntry(t *testing.T) {
	const body = `<MPD type="static" xmlns="urn:mpeg:dash:schema:mpd:2011"><Period duration="PT10S"><BaseURL>v/</BaseURL>
  <AdaptationSet contentType="video">
    <SegmentTemplate timescale="1000" media="$RepresentationID$-$Number%03d$.m4s" initialization="$RepresentationID$-init.mp4" startNumber="5">
      <SegmentTimeline><S t="0" d="2000" r="-1"/><S t="6000" d="4000"/></SegmentTimeline>
    </SegmentTemplate>
    <Representation id="hd" height="720"/>
  </AdaptationSet></Period></MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "hd", time.Time{})
	same(t, uris(w), []string{
		"https://cdn.example/live/v/hd-005.m4s", "https://cdn.example/live/v/hd-006.m4s",
		"https://cdn.example/live/v/hd-007.m4s", "https://cdn.example/live/v/hd-008.m4s",
	})
	if s := w.Segments[3]; s.Duration != 4*time.Second || s.Map == nil || s.Map.URI != "https://cdn.example/live/v/hd-init.mp4" || s.Place.Start != 6000 {
		t.Errorf("last segment = %+v", s)
	}
	if !w.Closed {
		t.Error("a static presentation's window is not closed")
	}
}

func TestATimedTemplateNamesSegmentsByTheirStart(t *testing.T) {
	const body = `<MPD type="static" xmlns="urn:mpeg:dash:schema:mpd:2011"><Period>
  <AdaptationSet contentType="audio"><SegmentTemplate timescale="48000" media="a/$Time$.m4s">
    <SegmentTimeline><S t="96000" d="96000" r="1"/></SegmentTimeline></SegmentTemplate>
    <Representation id="en"/></AdaptationSet></Period></MPD>`
	same(t, uris(window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackAudio, "en", time.Time{})),
		[]string{"https://cdn.example/live/a/96000.m4s", "https://cdn.example/live/a/192000.m4s"})
}

func TestALiveDurationTemplateListsOnlyWhatHasBeenPublished(t *testing.T) {
	const body = `<MPD type="dynamic" availabilityStartTime="2026-01-01T00:00:00Z" timeShiftBufferDepth="PT6S" xmlns="urn:mpeg:dash:schema:mpd:2011">
  <Period start="PT0S"><AdaptationSet contentType="video">
    <SegmentTemplate timescale="1" duration="2" media="$Number$.m4s" startNumber="1"/>
    <Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	// 21 seconds in, segments 1 to 10 have been produced; the buffer keeps the last three.
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", at(21))
	same(t, uris(w), []string{"https://cdn.example/live/8.m4s", "https://cdn.example/live/9.m4s", "https://cdn.example/live/10.m4s"})
	if w.Closed {
		t.Error("a live presentation's window is closed")
	}
}

// Packagers write the start with no zone or as +0000; either still places the edge, read as UTC.
func TestALiveStartWrittenWithoutARFC3339ZoneStillPlacesTheEdge(t *testing.T) {
	for _, start := range []string{"2026-01-01T00:00:00", "2026-01-01T00:00:00+0000"} {
		body := `<MPD type="dynamic" availabilityStartTime="` + start + `" timeShiftBufferDepth="PT6S" xmlns="urn:mpeg:dash:schema:mpd:2011">
  <Period start="PT0S"><AdaptationSet contentType="video">
    <SegmentTemplate timescale="1" duration="2" media="$Number$.m4s" startNumber="1"/>
    <Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
		w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", at(21))
		same(t, uris(w), []string{"https://cdn.example/live/8.m4s", "https://cdn.example/live/9.m4s", "https://cdn.example/live/10.m4s"})
	}
}

// sidx is a version-0 segment index box referencing subsegments of the given sizes, each a second at timescale 1000.
func sidx(first uint32, sizes ...uint32) []byte {
	var p []byte
	p = binary.BigEndian.AppendUint32(p, 0) // version and flags
	p = binary.BigEndian.AppendUint32(p, 1) // reference ID
	p = binary.BigEndian.AppendUint32(p, 1000)
	p = binary.BigEndian.AppendUint32(p, 0) // earliest presentation time
	p = binary.BigEndian.AppendUint32(p, first)
	p = binary.BigEndian.AppendUint16(p, 0)
	p = binary.BigEndian.AppendUint16(p, uint16(len(sizes)))
	for _, size := range sizes {
		p = binary.BigEndian.AppendUint32(p, size)
		p = binary.BigEndian.AppendUint32(p, 1000)
		p = binary.BigEndian.AppendUint32(p, 0x90000000)
	}
	return append(binary.BigEndian.AppendUint32(nil, uint32(8+len(p))), append([]byte("sidx"), p...)...)
}

func TestEachPeriodPlaysTheRepresentationNearestTheOneChosen(t *testing.T) {
	const body = `<MPD type="static" xmlns="urn:mpeg:dash:schema:mpd:2011">
  <Period id="film" duration="PT1S"><AdaptationSet contentType="video"><SegmentTemplate media="film/$RepresentationID$/$Number$.m4s" duration="1" startNumber="1"/>
    <Representation id="hd" height="720"/><Representation id="sd" height="360"/></AdaptationSet></Period>
  <Period id="ad" duration="PT1S"><AdaptationSet contentType="video"><SegmentTemplate media="ad/$RepresentationID$/$Number$.m4s" duration="1" startNumber="1"/>
    <Representation id="ad-1080" height="1080"/><Representation id="ad-480" height="480"/><Representation id="ad-240" height="240"/></AdaptationSet></Period>
  <Period id="film2" duration="PT1S"><AdaptationSet contentType="video"><SegmentTemplate media="film/$RepresentationID$/$Number$.m4s" duration="1" startNumber="2"/>
    <Representation id="hd" height="720"/></AdaptationSet></Period>
</MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "hd", time.Time{})
	same(t, uris(w), []string{"https://cdn.example/live/film/hd/1.m4s", "https://cdn.example/live/ad/ad-480/1.m4s", "https://cdn.example/live/film/hd/2.m4s"})
	if w.Segments[1].Place.Period != "ad" || w.Segments[2].Place.Period != "film2" {
		t.Errorf("segments are not keyed by their Period: %+v", w.Segments)
	}
}

func TestSoundKeepsItsLanguageAndLayoutAcrossPeriods(t *testing.T) {
	const body = `<MPD type="static" xmlns="urn:mpeg:dash:schema:mpd:2011">
  <Period id="film" duration="PT1S">
    <AdaptationSet contentType="audio" lang="fr"><SegmentTemplate media="film/$RepresentationID$/$Number$.m4s" duration="1"/>
      <Representation id="fr-51" codecs="mp4a.40.2" bandwidth="384000"><AudioChannelConfiguration schemeIdUri="urn:mpeg:mpegB:cicp:ChannelConfiguration" value="6"/></Representation>
    </AdaptationSet></Period>
  <Period id="next" duration="PT1S">
    <AdaptationSet contentType="audio" lang="en"><SegmentTemplate media="next/$RepresentationID$/$Number$.m4s" duration="1"/>
      <Representation id="en-51" codecs="mp4a.40.2" bandwidth="384000"><AudioChannelConfiguration schemeIdUri="urn:mpeg:mpegB:cicp:ChannelConfiguration" value="6"/></Representation></AdaptationSet>
    <AdaptationSet contentType="audio" lang="fr"><SegmentTemplate media="next/$RepresentationID$/$Number$.m4s" duration="1"/>
      <Representation id="fr-20" codecs="mp4a.40.2" bandwidth="512000"><AudioChannelConfiguration schemeIdUri="urn:mpeg:mpegB:cicp:ChannelConfiguration" value="2"/></Representation>
      <Representation id="fr-51b" codecs="mp4a.40.2" bandwidth="256000"><AudioChannelConfiguration schemeIdUri="urn:mpeg:mpegB:cicp:ChannelConfiguration" value="6"/></Representation>
    </AdaptationSet></Period>
</MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackAudio, "fr-51", time.Time{})
	same(t, uris(w), []string{"https://cdn.example/live/film/fr-51/1.m4s", "https://cdn.example/live/next/fr-51b/1.m4s"})
}

func TestAnIdIsTheRepresentationOfTheKindTheInputReads(t *testing.T) {
	const body = mpdOpen + `type="static">
  <Period id="ad" duration="PT1S"><AdaptationSet contentType="video"><SegmentTemplate media="ad/$RepresentationID$/$Number$.m4s" duration="1"/>
    <Representation id="1" height="720"/></AdaptationSet></Period>
  <Period id="film" duration="PT2S">
    <AdaptationSet contentType="video"><SegmentTemplate media="f/$RepresentationID$/$Number$.m4s" duration="1"/><Representation id="0" height="360"/></AdaptationSet>
    <AdaptationSet contentType="audio"><SegmentTemplate media="f/$RepresentationID$/$Number$.m4s" duration="1"/><Representation id="1" codecs="mp4a.40.2"/></AdaptationSet>
  </Period></MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackAudio, "1", time.Time{})
	same(t, uris(w), []string{"https://cdn.example/live/f/1/1.m4s", "https://cdn.example/live/f/1/2.m4s"})
}

func TestALiveTimelineListsOnlySegmentsThatExist(t *testing.T) {
	const body = mpdOpen + `type="dynamic" availabilityStartTime="2026-01-01T00:00:00Z" timeShiftBufferDepth="PT60S">
  <Period id="past" start="PT0S" duration="PT10S"><AdaptationSet contentType="video">
    <SegmentTemplate timescale="1" media="a$Time$.m4s"><SegmentTimeline><S t="0" d="2" r="-1"/></SegmentTimeline></SegmentTemplate>
    <Representation id="v" height="360"/></AdaptationSet></Period>
  <Period id="now" start="PT10S"><AdaptationSet contentType="video">
    <SegmentTemplate timescale="1" media="b$Time$.m4s"><SegmentTimeline><S t="0" d="2" r="-1"/></SegmentTimeline></SegmentTemplate>
    <Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	// Fifteen seconds in, the ended Period holds five segments and the running one two finished and one in production.
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", at(15))
	same(t, uris(w), []string{
		"https://cdn.example/live/a0.m4s", "https://cdn.example/live/a2.m4s", "https://cdn.example/live/a4.m4s",
		"https://cdn.example/live/a6.m4s", "https://cdn.example/live/a8.m4s",
		"https://cdn.example/live/b0.m4s", "https://cdn.example/live/b2.m4s",
	})
}

func TestAFiniteLiveTimelineDoesNotPublishFutureSegments(t *testing.T) {
	const body = mpdOpen + `type="dynamic" availabilityStartTime="2026-01-01T00:00:00Z" timeShiftBufferDepth="PT6S">
  <Period><AdaptationSet contentType="video"><SegmentTemplate timescale="1" media="$Time$.m4s">
    <SegmentTimeline><S t="0" d="2" r="20"/></SegmentTimeline></SegmentTemplate>
    <Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", at(21))
	same(t, uris(w), []string{"https://cdn.example/live/14.m4s", "https://cdn.example/live/16.m4s", "https://cdn.example/live/18.m4s"})
}

func TestATimelineStopsListingAtThePeriodEnd(t *testing.T) {
	const body = mpdOpen + `type="static"><Period duration="PT3S"><AdaptationSet contentType="video">
  <SegmentTemplate timescale="1" media="$Time$.m4s"><SegmentTimeline><S t="0" d="2" r="3"/></SegmentTimeline></SegmentTemplate>
  <Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", time.Time{})
	same(t, uris(w), []string{"https://cdn.example/live/0.m4s", "https://cdn.example/live/2.m4s"})
}

func TestALiveDurationTemplateForgetsWhatTheBufferNoLongerKeeps(t *testing.T) {
	const body = mpdOpen + `type="dynamic" availabilityStartTime="2026-01-01T00:00:00Z" timeShiftBufferDepth="PT6S">
  <Period id="old" start="PT0S" duration="PT6S"><AdaptationSet contentType="video">
    <SegmentTemplate timescale="1" duration="2" media="old$Number$.m4s"/><Representation id="v" height="360"/></AdaptationSet></Period>
  <Period id="now" start="PT600S"><AdaptationSet contentType="video">
    <SegmentTemplate timescale="1" duration="2" media="now$Number$.m4s"/><Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", at(605))
	same(t, uris(w), []string{"https://cdn.example/live/now1.m4s", "https://cdn.example/live/now2.m4s"})
}

func TestAnOffsetAvailabilityListsTheSegmentStillInProduction(t *testing.T) {
	const body = mpdOpen + `type="dynamic" availabilityStartTime="2026-01-01T00:00:00Z" timeShiftBufferDepth="PT60S">
  <Period start="PT0S"><AdaptationSet contentType="video">
    <SegmentTemplate timescale="1" duration="2" media="$Number$.m4s" availabilityTimeOffset="INF"/><Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	same(t, uris(window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", at(5))),
		[]string{"https://cdn.example/live/1.m4s", "https://cdn.example/live/2.m4s", "https://cdn.example/live/3.m4s"})
}

func TestALivePresentationWithNoStartIsRefused(t *testing.T) {
	const body = mpdOpen + `type="dynamic"><Period><AdaptationSet contentType="video">
    <SegmentTemplate timescale="1" duration="2" media="$Number$.m4s"/><Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	in := media.Input{ID: media.PrimaryInputID, URL: sourcetest.URL(t, "https://cdn.example/live/manifest.mpd"), Representation: "v"}
	f := Format{}.Timeline(&sourcetest.Document{Body: body, Status: http.StatusOK}, in, media.TrackVideo)
	if _, err := f.Window(t.Context()); err == nil {
		t.Error("placed a live edge with no availabilityStartTime")
	}
}

func TestAListInheritsFromItsSetAttributeByAttribute(t *testing.T) {
	const body = mpdOpen + `type="static"><Period><AdaptationSet contentType="video">
  <SegmentList timescale="10" duration="20"><Initialization sourceURL="init.mp4"/></SegmentList>
  <Representation id="v" height="360"><SegmentList><SegmentURL media="1.m4s"/><SegmentURL media="2.m4s"/></SegmentList></Representation>
  </AdaptationSet></Period></MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", time.Time{})
	if len(w.Segments) != 2 || w.Segments[1].Duration != 2*time.Second || w.Segments[1].Map == nil || w.Segments[1].Map.URI != "https://cdn.example/live/init.mp4" {
		t.Errorf("segments = %+v, want two 2s segments under the set's init", w.Segments)
	}
}

func TestATemplatesInitializationElementNamesItsInit(t *testing.T) {
	const body = mpdOpen + `type="static"><Period duration="PT2S"><AdaptationSet contentType="video">
  <SegmentTemplate media="$Number$.m4s" duration="1"><Initialization sourceURL="head.mp4"/></SegmentTemplate>
  <Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "v", time.Time{})
	if len(w.Segments) != 2 || w.Segments[0].Map == nil || w.Segments[0].Map.URI != "https://cdn.example/live/head.mp4" {
		t.Errorf("segments = %+v, want the Initialization element's init", w.Segments)
	}
}

func TestAnIndexWithNoStatedInitTakesWhatPrecedesIt(t *testing.T) {
	index := sidx(0, 1000)
	film := make([]byte, 700)
	copy(film[600:], index)
	docs := map[string]string{
		"/live/manifest.mpd": mpdOpen + `type="static"><Period><AdaptationSet contentType="video" mimeType="video/mp4">
  <Representation id="v" height="360"><BaseURL>film.mp4</BaseURL><SegmentBase indexRange="600-651"/></Representation>
  </AdaptationSet></Period></MPD>`,
		"/live/film.mp4": string(film),
	}
	w := window(t, docs, media.TrackVideo, "v", time.Time{})
	if len(w.Segments) != 1 || w.Segments[0].Map == nil || w.Segments[0].Map.Range.Length != 600 {
		t.Errorf("segments = %+v, want the 600 bytes ahead of the index as the init", w.Segments)
	}
}

func TestARepresentationNamingNothingIsRefused(t *testing.T) {
	const body = mpdOpen + `type="static"><Period duration="PT2S"><AdaptationSet contentType="video"><Representation id="v" height="360"/></AdaptationSet></Period></MPD>`
	in := media.Input{ID: media.PrimaryInputID, URL: sourcetest.URL(t, "https://cdn.example/live/manifest.mpd"), Representation: "v"}
	f := Format{}.Timeline(&sourcetest.Document{Body: body, Status: http.StatusOK}, in, media.TrackVideo)
	if _, err := f.Window(t.Context()); err == nil {
		t.Error("read the presentation itself as the representation's media")
	}
}

func TestAnIdReusedAcrossPeriodsIsReadAsTheOneChosen(t *testing.T) {
	const body = mpdOpen + `type="static">
  <Period id="ad" duration="PT1S"><AdaptationSet contentType="video"><SegmentTemplate media="ad/$RepresentationID$/$Number$.m4s" duration="1"/>
    <Representation id="0" height="1080"/></AdaptationSet></Period>
  <Period id="film" duration="PT4S"><AdaptationSet contentType="video"><SegmentTemplate media="f/$RepresentationID$/$Number$.m4s" duration="4"/>
    <Representation id="0" height="360"/><Representation id="1" height="720"/></AdaptationSet></Period>
  <Period id="ad2" duration="PT1S"><AdaptationSet contentType="video"><SegmentTemplate media="ad2/$RepresentationID$/$Number$.m4s" duration="1"/>
    <Representation id="x" height="1080"/><Representation id="y" height="360"/></AdaptationSet></Period>
</MPD>`
	w := window(t, map[string]string{"/live/manifest.mpd": body}, media.TrackVideo, "0", time.Time{})
	same(t, uris(w), []string{"https://cdn.example/live/ad/0/1.m4s", "https://cdn.example/live/f/0/1.m4s", "https://cdn.example/live/ad2/y/1.m4s"})
}
