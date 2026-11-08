package housemap

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteSVG_ExampleIsWellFormed(t *testing.T) {
	m := exampleMap(t)
	var buf bytes.Buffer
	require.NoError(t, m.WriteSVG(&buf))

	dec := xml.NewDecoder(&buf)
	elements := map[string]int{}
	var root string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if se, ok := tok.(xml.StartElement); ok {
			if root == "" {
				root = se.Name.Local
			}
			elements[se.Name.Local]++
		}
	}
	assert.Equal(t, "svg", root)
	assert.Equal(t, len(m.Spaces), elements["polygon"], "one polygon per space")
	assert.Equal(t, len(m.Doors)+1, elements["line"], "doors, plus the scale bar")
	assert.Equal(t, len(m.Lines), elements["polyline"])
	assert.Equal(t, len(m.Cameras), elements["circle"])
}

func TestWriteSVG_EscapesNamesAndDrawsFullCircles(t *testing.T) {
	m := &Map{
		Spaces: map[string]*Space{"a<b": {Kind: KindYard, Polygon: []Point{{0, 0}, {1, 0}, {1, 1}}}},
		Doors: map[string]*Door{
			"unplaced": {Kind: KindDoor, Connects: []string{"a<b", "x"}},
			"wide":     {Kind: KindOpening, Connects: []string{"a<b", "x"}, Span: []Point{{0, 0}, {1, 0}}},
		},
		Cameras: map[string]*Camera{"all": {At: Point{0, 0}, FovDeg: 360, RangeM: 3},
			"wide": {At: Point{1, 1}, FovDeg: 270, RangeM: 3}},
	}
	var buf bytes.Buffer
	require.NoError(t, m.WriteSVG(&buf))
	assert.Contains(t, buf.String(), "a&lt;b")
	assert.NotContains(t, buf.String(), "<title>unplaced</title>")
	assert.Contains(t, buf.String(), " 0 1 1 ", "a view wider than 180 degrees uses the large arc")
	dec := xml.NewDecoder(&buf)
	for {
		if _, err := dec.Token(); err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
}

func TestWriteReviewHTML_Example(t *testing.T) {
	m := exampleMap(t)
	var buf bytes.Buffer
	seen := map[string]bool{"lounge": true, "kitchen": true, "hall": true}
	require.NoError(t, m.WriteReviewHTML(&buf, ReviewOptions{Source: "testdata/example_house_map.yaml", Seen: seen}))
	page := buf.String()

	assert.True(t, strings.HasPrefix(page, "<title>"), "an artifact fragment starts with <title>")
	for _, tag := range []string{"<html", "<head>", "<head ", "<body"} {
		assert.NotContains(t, page, tag)
	}
	for name := range m.Spaces {
		assert.Contains(t, page, "<title>"+name+" ", "space %s", name)
	}
	for name := range m.Doors {
		assert.Contains(t, page, "<code>"+name+"</code>", "door %s", name)
	}
	for name := range m.Cameras {
		assert.Contains(t, page, "<title>"+name+"</title>", "camera %s", name)
	}
	assert.Contains(t, page, "<code>porch_cam</code>", "the outdoor camera is in the table")
	assert.NotContains(t, page, "<code>cam_hall</code>", "indoor cameras are not in the outdoor table")
	assert.Contains(t, page, "<code>study</code> <em>unseen</em>")
	assert.NotContains(t, page, "<code>lounge</code> <em>unseen</em>")
	assert.Contains(t, page, "<code>hall_to_kitchen</code>")
	assert.Contains(t, page, "6 doors and openings")
	assert.NotContains(t, page, "Still to place", "every example door is placed")

	for _, url := range []string{"http://", "https://"} {
		for _, rest := range strings.Split(page, url)[1:] {
			assert.True(t, strings.HasPrefix(rest, "fonts.googleapis.com") || strings.HasPrefix(rest, "www.w3.org"),
				"only Google Fonts may be loaded: %.40s", rest)
		}
	}
}

func TestWriteReviewHTML_EscapesAndListsUnplaced(t *testing.T) {
	m := exampleMap(t)
	m.Doors["side"] = &Door{Kind: KindDoor, Connects: []string{"kitchen", "yard"}, Lock: "lock.<side>"}
	m.Cameras["porch_cam"].Note = `<script>alert("x")</script>`
	var buf bytes.Buffer
	require.NoError(t, m.WriteReviewHTML(&buf, ReviewOptions{Source: "x"}))
	page := buf.String()
	assert.Contains(t, page, "Still to place")
	assert.Contains(t, page, "<code>side</code>: kitchen ↔ yard · lock <code>lock.&lt;side&gt;</code>")
	assert.NotContains(t, page, `<script>alert`)
	assert.NotContains(t, page, "<em>unseen</em>", "no room presence config marks nothing unseen")
}

func TestAreaHueIsStable(t *testing.T) {
	assert.Equal(t, AreaHue("kitchen"), AreaHue("kitchen"))
	assert.NotEqual(t, AreaHue("kitchen"), AreaHue("lounge"))
	for _, a := range []string{"", "a", "kitchen", "bedroom"} {
		assert.GreaterOrEqual(t, AreaHue(a), 0)
		assert.Less(t, AreaHue(a), 360)
	}
}
