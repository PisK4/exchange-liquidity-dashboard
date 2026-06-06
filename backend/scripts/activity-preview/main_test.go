package main

import "testing"

func TestParseCardSelectionMatrix(t *testing.T) {
	cases := []struct {
		raw     string
		want    []string
		wantErr bool
	}{
		{raw: "", want: allActivityCards},
		{raw: "all", want: allActivityCards},
		{raw: "daily,event,update,review,weekly,health", want: allActivityCards},
		{raw: "daily", want: []string{"daily"}},
		{raw: "review,health", want: []string{"review", "health"}},
		{raw: "garbage", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := parseCardSelection(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got=%v want=%v", got, tc.want)
				}
			}
		})
	}
}

func TestBuildPreviewCardsCoversSixActivityCards(t *testing.T) {
	cards, err := buildPreviewCards(allActivityCards, "https://dashboard.example.test", "secret")
	if err != nil {
		t.Fatalf("buildPreviewCards err=%v", err)
	}
	if len(cards) != 6 {
		t.Fatalf("cards len=%d want 6", len(cards))
	}
	seen := map[string]bool{}
	for _, card := range cards {
		seen[card.Name] = true
		if len(card.Payload) == 0 {
			t.Fatalf("empty payload for %s", card.Name)
		}
	}
	for _, name := range allActivityCards {
		if !seen[name] {
			t.Fatalf("missing preview card %q; seen=%v", name, seen)
		}
	}
}
