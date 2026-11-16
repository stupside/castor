package ssdp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func answer(usn, location string) *http.Response {
	return &http.Response{Header: http.Header{"Usn": {usn}, "Location": {location}}}
}

func TestOneAddressThatCannotSendDoesNotHideTheOthers(t *testing.T) {
	found, err := searchFromEach(t.Context(), []string{"10.0.0.2", "172.17.0.1", "192.168.1.2"}, func(_ context.Context, address string) ([]*http.Response, error) {
		if address == "172.17.0.1" {
			return nil, errors.New("sendto: no route to host")
		}
		return []*http.Response{answer("uuid:tv-"+address, "http://"+address+"/")}, nil
	})
	if err != nil || len(found) != 2 {
		t.Fatalf("search = %d answers, %v; want the two addresses that could send", len(found), err)
	}
}

func TestASearchThatSentFromNoAddressIsAnError(t *testing.T) {
	_, err := searchFromEach(t.Context(), []string{"10.0.0.2", "192.168.1.2"}, func(context.Context, string) ([]*http.Response, error) {
		return nil, errors.New("sendto: network is down")
	})
	if err == nil {
		t.Fatal("a search that sent nothing reported no error")
	}
}

func TestADeviceAnsweringOnTwoAddressesIsOneAnswer(t *testing.T) {
	found, err := searchFromEach(t.Context(), []string{"10.0.0.2", "192.168.1.2"}, func(_ context.Context, address string) ([]*http.Response, error) {
		return []*http.Response{answer("uuid:tv", "http://"+address+":9197/dmr")}, nil
	})
	if err != nil || len(found) != 1 {
		t.Fatalf("search = %d answers, %v; want one", len(found), err)
	}
}

func TestASearchLeavesAThirdOfTheCallersTimeToReadTheAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, tt := range []struct {
			caller, most, want time.Duration
		}{
			{caller: 5 * time.Second, most: 2 * time.Second, want: 2 * time.Second},
			{caller: 3 * time.Second, most: 3 * time.Second, want: 2 * time.Second},
			{caller: 5 * time.Second, most: 10 * time.Second, want: 5 * time.Second * 2 / 3},
		} {
			ctx, cancel := context.WithTimeout(t.Context(), tt.caller)
			if got := window(ctx, tt.most); got != tt.want {
				t.Errorf("window(%v, %v) = %v, want %v", tt.caller, tt.most, got, tt.want)
			}
			cancel()
		}
		if got := window(t.Context(), 2*time.Second); got != 2*time.Second {
			t.Errorf("without a deadline the search got %v", got)
		}
	})
}
