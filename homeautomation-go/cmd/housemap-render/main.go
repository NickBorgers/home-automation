// Command housemap-render draws a house map as an SVG picture or as an HTML review page.
//
//	go run ./cmd/housemap-render -in configs/house_map.yaml -out house_map.svg
//	go run ./cmd/housemap-render -format html -in configs/house_map.yaml \
//	    -room-presence configs/room_presence_config.yaml -out house_map.html
//
// See docs/architecture/HOUSE_MAP.md.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"homeautomation/internal/housemap"
	"homeautomation/internal/plugins/roompresence"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "housemap-render:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("housemap-render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "house map YAML file (required)")
	out := fs.String("out", "", "file to write (required)")
	format := fs.String("format", "svg", "svg, or html for a review page to publish as an artifact")
	rpPath := fs.String("room-presence", "", "room presence config, to mark unseen areas on the html page (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" {
		fs.Usage()
		return errors.New("-in and -out are required")
	}
	if *format != "svg" && *format != "html" {
		return fmt.Errorf("-format %q must be svg or html", *format)
	}
	m, err := housemap.Load(*in)
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("%s does not exist", *in)
	}

	var buf bytes.Buffer
	if *format == "svg" {
		err = m.WriteSVG(&buf)
	} else {
		opts := housemap.ReviewOptions{Source: *in}
		if *rpPath != "" {
			rp, err := roompresence.LoadConfig(*rpPath)
			if err != nil {
				return err
			}
			if rp == nil {
				return fmt.Errorf("%s does not exist", *rpPath)
			}
			opts.Seen = map[string]bool{}
			for name, a := range rp.Areas {
				opts.Seen[name] = a.Seen
			}
		}
		err = m.WriteReviewHTML(&buf, opts)
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	return nil
}
