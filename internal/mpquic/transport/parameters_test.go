package transport

import (
	"strings"
	"testing"
)

func TestParametersValidate(t *testing.T) {
	tests := []struct {
		name        string
		params      Parameters
		wantErr     bool
		errContains string
	}{
		{
			name: "valid when max path id is greater than initial max path id",
			params: Parameters{
				InitialMaxPathID: 1,
				MaxPathID:        4,
			},
			wantErr: false,
		},
		{
			name: "valid when max path id equals initial max path id",
			params: Parameters{
				InitialMaxPathID: 2,
				MaxPathID:        2,
			},
			wantErr: false,
		},
		{
			name: "invalid when max path id is below initial max path id",
			params: Parameters{
				InitialMaxPathID: 2,
				MaxPathID:        1,
			},
			wantErr:     true,
			errContains: "max path id",
		},
		{
			name: "valid single initial path only",
			params: Parameters{
				InitialMaxPathID: 0,
				MaxPathID:        0,
			},
			wantErr: false,
		},
		{
			name: "valid zero initial with later path expansion",
			params: Parameters{
				InitialMaxPathID: 0,
				MaxPathID:        4,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate()

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected validation error for params %#v", tt.params)
				}
				if tt.errContains != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errContains)) {
					t.Fatalf("expected error to contain %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected validation error for params %#v: %v", tt.params, err)
			}
		})
	}
}