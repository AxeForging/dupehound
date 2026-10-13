package helpers

import "testing"

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"0", 0, false},
		{"1024", 1024, false},
		{"512K", 512 << 10, false},
		{"512k", 512 << 10, false},
		{"64M", 64 << 20, false},
		{"2G", 2 << 30, false},
		{"2GB", 2 << 30, false},
		{"2GiB", 2 << 30, false},
		{"1.5G", 3 << 29, false},
		{" 2g ", 2 << 30, false},
		{"", 0, true},
		{"G", 0, true},
		{"-1G", 0, true},
		{"2X", 0, true},
		{"two", 0, true},
		{"99999999999G", 0, true}, // overflows int64
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseByteSize(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseByteSize(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseByteSize(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
