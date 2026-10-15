package flatkeys_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/flatkeys"
)

var _ cel2filter.Encoder = flatkeys.Encoder{}

func TestRoundTrip(t *testing.T) {
	labels := map[string]map[string]any{
		"Classification": {"Level": "Confidential", "Owners": []string{"alice", "bob"}, "Single": []string{"one"}, "Empty": []string{}},
		"A/B=C\\D":       {"F/G": "v=w/x", "Count": "3"},
		"Archive":        {},
	}
	flat := flatkeys.Flatten(labels)
	want := []string{
		"A\\/B\\=C\\\\D", "A\\/B\\=C\\\\D/Count", "A\\/B\\=C\\\\D/Count=3", "A\\/B\\=C\\\\D/F\\/G", "A\\/B\\=C\\\\D/F\\/G=v=w/x",
		"Archive",
		"Classification", "Classification/Empty", "Classification/Empty/", "Classification/Level", "Classification/Level=Confidential",
		"Classification/Owners", "Classification/Owners/", "Classification/Owners=alice", "Classification/Owners=bob",
		"Classification/Single", "Classification/Single/", "Classification/Single=one",
	}
	require.Equal(t, want, flat)
	require.Equal(t, labels, flatkeys.Unflatten(flat))
	require.Nil(t, flatkeys.Flatten(nil))
	require.Nil(t, flatkeys.Unflatten(nil))
	require.Equal(t, map[string]map[string]any{"L": {}}, flatkeys.Unflatten([]string{"L", "L/", "", "/x"}))
	require.Equal(t, map[string]map[string]any{"L": {"F": []string{"a", "b"}}}, flatkeys.Unflatten([]string{"L/F=a", "L/F=b"}))
	require.Equal(t, []string{"L", "L/F", "L/F/", "L/F=1", "L/F=x"}, flatkeys.Flatten(map[string]map[string]any{"L": {"F": []any{int64(1), "x"}}}))
}

func TestEntryAndCanon(t *testing.T) {
	v := "x/y=z"
	require.Equal(t, "A\\/B\\=C\\\\D/F\\/G", flatkeys.Entry("A/B=C\\D", "F/G", nil))
	require.Equal(t, "L/F=x/y=z", flatkeys.Entry("L", "F", &v))
	require.Equal(t, "L", flatkeys.Entry("L", "", nil))
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for want, in := range map[string]any{"s": "s", "true": true, "3": 3, "4": int64(4), "5": 5.0, "2.5": 2.5, "2025-01-01T00:00:00Z": ts, "": struct{}{}} {
		require.Equal(t, want, flatkeys.Canon(in))
	}
}

func TestEncode(t *testing.T) {
	enc := flatkeys.Encoder{}
	cases := []struct {
		keys  []string
		value any
		want  string
		ok    bool
	}{
		{[]string{"Classification"}, nil, "Classification", true},
		{[]string{"Classification", "Level"}, nil, "Classification/Level", true},
		{[]string{"Classification", "Level"}, "Confidential", "Classification/Level=Confidential", true},
		{[]string{"Classification", "Count"}, int64(3), "Classification/Count=3", true},
		{[]string{"Classification", "Flag"}, true, "Classification/Flag=true", true},
		{[]string{"A/B", "C=D"}, "x/y=z", "A\\/B/C\\=D=x/y=z", true},
		{[]string{"Classification"}, "v", "", false},
		{[]string{"a", "b", "c"}, nil, "", false},
		{nil, nil, "", false},
		{[]string{"a", "b"}, struct{}{}, "", false},
	}
	for _, c := range cases {
		got, ok := enc.Encode(c.keys, c.value)
		require.Equal(t, c.ok, ok, "%v %v", c.keys, c.value)
		require.Equal(t, c.want, got)
	}
}
