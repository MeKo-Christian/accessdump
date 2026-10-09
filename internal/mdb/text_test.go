package mdb

import "testing"

func TestDecodeJet4Text(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{
			name: "plain UCS-2",
			in:   []byte{'G', 0, 0xF6, 0, 0xDF, 0, 'e', 0},
			want: "Göße",
		},
		{
			name: "compressed with umlauts",
			in:   []byte{0xFF, 0xFE, 'G', 'r', 0xF6, 0xDF, 'e'},
			want: "Größe",
		},
		{
			// "A€B": the euro sign lies above U+00FF, so the run switches to
			// UCS-2 for it and back.
			name: "compressed with an uncompressed run",
			in:   []byte{0xFF, 0xFE, 'A', 0x00, 0xAC, 0x20, 0x00, 'B'},
			want: "A€B",
		},
		{
			name: "only the marker",
			in:   []byte{0xFF, 0xFE},
			want: "",
		},
		{
			name: "empty",
			in:   nil,
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := decodeJet4Text(test.in); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}
