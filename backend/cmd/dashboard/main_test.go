package main

import "testing"

func TestRoleStartsLiveProviders(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{role: "api", want: false},
		{role: "collector", want: true},
		{role: "all", want: true},
		{role: "", want: false},
	}

	for _, tt := range tests {
		if got := roleStartsLiveProviders(tt.role); got != tt.want {
			t.Fatalf("roleStartsLiveProviders(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}
