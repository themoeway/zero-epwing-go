package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"path"

	zig "github.com/FooSoft/zero-epwing-go"
)

type Entry struct {
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

type Subbook struct {
	Title     string  `json:"title"`
	Copyright string  `json:"copyrignt"`
	Entries   []Entry `json:"entries"`
}

type Book struct {
	DiscCode string    `json:"discCode"`
	CharCode string    `json:"charCode"`
	Subbooks []Subbook `json:"subbooks"`
}

func outputEntries(bookSrc *zig.Book, path string, pretty bool) error {
	bookDst := Book{
		DiscCode: bookSrc.DiscCode,
		CharCode: bookSrc.CharCode,
	}

	for _, subbookSrc := range bookSrc.Subbooks {
		subbookDst := Subbook{
			Title:     subbookSrc.Title,
			Copyright: subbookSrc.Copyright,
		}

		for _, entrySrc := range subbookSrc.Entries {
			entryDst := Entry{
				Heading: entrySrc.Heading,
				Text:    entrySrc.Text,
			}

			subbookDst.Entries = append(subbookDst.Entries, entryDst)
		}

		bookDst.Subbooks = append(bookDst.Subbooks, subbookDst)
	}

	var (
		data []byte
		err  error
	)

	if pretty {
		data, err = json.MarshalIndent(bookDst, "", "\t")
	} else {
		data, err = json.Marshal(bookDst)
	}

	if err != nil {
		return err
	}

	return ioutil.WriteFile(path, data, 0644)
}

func outputGaiji(bookSrc *zig.Book, gaiji16Dir, gaiji24Dir, gaiji30Dir, gaiji48Dir string) error {
	for subbookIndex, subbook := range bookSrc.Subbooks {
		outputGaijiSet := func(gaijiType string, mapping map[int]zig.Gaiji) error {
			for codepoint, gaiji := range mapping {
				outputGaijiSingle := func(gaijiDir string, gaijiGlyph image.Image) error {
					if len(gaijiDir) == 0 {
						return nil
					}

					gaijiPath := path.Join(gaijiDir, fmt.Sprintf("%d_%d_%s_%d.png", subbookIndex, codepoint, gaijiType, gaijiGlyph.Bounds().Dy()))
					fp, err := os.Create(gaijiPath)
					if err != nil {
						return err
					}

					defer fp.Close()
					return png.Encode(fp, gaijiGlyph)
				}

				if err := outputGaijiSingle(gaiji16Dir, gaiji.Glyph16); err != nil {
					return err
				}
				if err := outputGaijiSingle(gaiji24Dir, gaiji.Glyph24); err != nil {
					return err
				}
				if err := outputGaijiSingle(gaiji30Dir, gaiji.Glyph30); err != nil {
					return err
				}
				if err := outputGaijiSingle(gaiji48Dir, gaiji.Glyph48); err != nil {
					return err
				}
			}

			return nil
		}

		if err := outputGaijiSet("n", subbook.GaijiNarrow); err != nil {
			return err
		}

		if err := outputGaijiSet("w", subbook.GaijiWide); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	var (
		gaiji16Dir    = flag.String("gaiji16-dir", "", "output directory for gaiji glyphs (size 16)")
		gaiji24Dir    = flag.String("gaiji24-dir", "", "output directory for gaiji glyphs (size 24)")
		gaiji30Dir    = flag.String("gaiji30-dir", "", "output directory for gaiji glyphs (size 30)")
		gaiji48Dir    = flag.String("gaiji48-dir", "", "output directory for gaiji glyphs (size 48)")
		entriesPath   = flag.String("entries-path", "", "output path for dictionary entries")
		entriesPretty = flag.Bool("entries-pretty", false, "pretty-print dictionary entries")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: zero-epwing [options] path\n")
		fmt.Fprintf(os.Stderr, "Parameters:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		flag.Usage()
		os.Exit(2)
	}

	var flags zig.LoadFlags
	if len(*gaiji16Dir) > 0 {
		flags |= zig.LoadFlagsGaiji16
	}
	if len(*gaiji24Dir) > 0 {
		flags |= zig.LoadFlagsGaiji24
	}
	if len(*gaiji30Dir) > 0 {
		flags |= zig.LoadFlagsGaiji30
	}
	if len(*gaiji48Dir) > 0 {
		flags |= zig.LoadFlagsGaiji48
	}

	book, err := zig.Load(args[0], flags)
	if err != nil {
		log.Fatal(err)
	}

	if len(*entriesPath) > 0 {
		if err := outputEntries(book, *entriesPath, *entriesPretty); err != nil {
			log.Fatal(err)
		}
	}

	if err := outputGaiji(book, *gaiji16Dir, *gaiji24Dir, *gaiji30Dir, *gaiji48Dir); err != nil {
		log.Fatal(err)
	}
}
