package zig

import (
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"sync"
	"unsafe"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
)

/*
#cgo linux LDFLAGS: -lz
#cgo windows LDFLAGS: -lz -lws2_32
#include "zig.h"
*/
import "C"

type blockType int

const (
	blockTypeHeading blockType = iota
	blockTypeText
)

type fontType int

const (
	fontTypeNarrow fontType = iota
	fontTypeWide
)

type LoadFlags int

const (
	LoadFlagsNone LoadFlags = 0

	LoadFlagsStubGaiji = 1 << iota

	LoadFlagsGaiji16
	LoadFlagsGaiji24
	LoadFlagsGaiji30
	LoadFlagsGaiji48
)

var (
	activeSubbookContext *subbookContext
	activeSubbookLock    sync.Mutex
)

func setSubbookContext(sc *subbookContext) {
	activeSubbookLock.Lock()
	activeSubbookContext = sc
}

func clearSubbookContext() {
	activeSubbookContext = nil
	activeSubbookLock.Unlock()
}

type subbookContext struct {
	blocksSeen       map[uint32]bool
	codepointsWide   map[int]bool
	codepointsNarrow map[int]bool
	flags            LoadFlags
}

//export hookCallback
func hookCallback(book *C.EB_Book, appendix *C.EB_Appendix, container *C.void, hookCode C.EB_Hook_Code, argc C.int, argv *C.uint) C.EB_Error_Code {
	var marker string
	switch hookCode {
	case C.EB_HOOK_NARROW_FONT:
		activeSubbookContext.codepointsNarrow[int(*argv)] = true
		if activeSubbookContext.flags&LoadFlagsStubGaiji != 0 {
			marker = fmt.Sprintf("{{n_%d}}", *argv)
		}
	case C.EB_HOOK_WIDE_FONT:
		activeSubbookContext.codepointsWide[int(*argv)] = true
		if activeSubbookContext.flags&LoadFlagsStubGaiji != 0 {
			marker = fmt.Sprintf("{{w_%d}}", *argv)
		}
	}

	if len(marker) > 0 {
		markerC := C.CString(marker)
		defer C.free(unsafe.Pointer(markerC))
		C.eb_write_text_string(book, markerC)
	}

	return C.EB_SUCCESS
}

func formatError(code C.EB_Error_Code) string {
	return C.GoString(C.eb_error_string(code))
}

type Gaiji struct {
	Glyph16 image.Image
	Glyph24 image.Image
	Glyph30 image.Image
	Glyph48 image.Image
}

type Entry struct {
	Heading string
	Text    string
}

type Subbook struct {
	Title       string
	Copyright   string
	Entries     []Entry
	GaijiWide   map[int]Gaiji
	GaijiNarrow map[int]Gaiji
}

type Book struct {
	DiscCode string
	CharCode string
	Subbooks []Subbook
}

type bookContext struct {
	buffer  []byte
	decoder *encoding.Decoder
	hookset *C.EB_Hookset
	book    *C.EB_Book
	flags   LoadFlags
}

func (bc *bookContext) initialize() error {
	if errEb := C.eb_initialize_library(); errEb != C.EB_SUCCESS {
		return fmt.Errorf("eb_initialize_library failed with code: %s", formatError(errEb))
	}

	bc.book = (*C.EB_Book)(C.calloc(1, C.size_t(unsafe.Sizeof(C.EB_Book{}))))
	C.eb_initialize_book(bc.book)

	bc.hookset = (*C.EB_Hookset)(C.calloc(1, C.size_t(unsafe.Sizeof(C.EB_Hookset{}))))
	C.eb_initialize_hookset(bc.hookset)

	if err := bc.installHooks(); err != nil {
		return err
	}

	bc.buffer = make([]byte, 22)
	bc.decoder = japanese.EUCJP.NewDecoder()

	return nil
}

func (bc *bookContext) shutdown() {
	C.eb_finalize_hookset(bc.hookset)
	C.free(unsafe.Pointer(bc.hookset))

	C.eb_finalize_book(bc.book)
	C.free(unsafe.Pointer(bc.book))

	C.eb_finalize_library()
}

func (bc *bookContext) installHooks() error {
	hookCodes := []C.EB_Hook_Code{
		C.EB_HOOK_NARROW_FONT,
		C.EB_HOOK_WIDE_FONT,
	}

	for _, hookCode := range hookCodes {
		if errEb := C.installHook(bc.hookset, hookCode); errEb != C.EB_SUCCESS {
			return fmt.Errorf("eb_set_hook failed with code: %s", formatError(errEb))
		}
	}

	return nil
}

func (bc *bookContext) loadInternal(path string) (*Book, error) {
	pathC := C.CString(path)
	defer C.free(unsafe.Pointer(pathC))
	if errEb := C.eb_bind(bc.book, pathC); errEb != C.EB_SUCCESS {
		return nil, fmt.Errorf("eb_bind failed with code: %s", formatError(errEb))
	}

	var (
		book Book
		err  error
	)

	if book.CharCode, err = bc.loadCharCode(); err != nil {
		return nil, err
	}

	if book.DiscCode, err = bc.loadDiscCode(); err != nil {
		return nil, err
	}

	if book.Subbooks, err = bc.loadSubbooks(); err != nil {
		return nil, err
	}

	return &book, nil
}

func (bc *bookContext) loadCharCode() (string, error) {
	var charCode C.EB_Character_Code
	if errEb := C.eb_character_code(bc.book, &charCode); errEb != C.EB_SUCCESS {
		return "", fmt.Errorf("eb_character_code failed with code: %s", formatError(errEb))
	}

	switch charCode {
	case C.EB_CHARCODE_ISO8859_1:
		return "iso8859-1", nil
	case C.EB_CHARCODE_JISX0208:
		return "jisx0208", nil
	case C.EB_CHARCODE_JISX0208_GB2312:
		return "jisx0208/gb2312", nil
	default:
		return "invalid", nil
	}
}

func (bc *bookContext) loadDiscCode() (string, error) {
	var discCode C.EB_Disc_Code
	if errEb := C.eb_disc_type(bc.book, &discCode); errEb != C.EB_SUCCESS {
		return "", fmt.Errorf("eb_disc_type failed with code: %s", formatError(errEb))
	}

	switch discCode {
	case C.EB_DISC_EB:
		return "eb", nil
	case C.EB_DISC_EPWING:
		return "epwing", nil
	default:
		return "invalid", nil
	}
}

func (bc *bookContext) loadSubbooks() ([]Subbook, error) {
	var (
		subbookCodes [C.EB_MAX_SUBBOOKS]C.EB_Subbook_Code
		subbookCount C.int
	)

	if errEb := C.eb_subbook_list(bc.book, &subbookCodes[0], &subbookCount); errEb != C.EB_SUCCESS {
		return nil, fmt.Errorf("eb_subbook_list failed with code: %s", formatError(errEb))
	}

	var subbooks []Subbook
	for i := 0; i < int(subbookCount); i++ {
		subbook, err := bc.loadSubbook(subbookCodes[i])
		if err != nil {
			return nil, err
		}

		subbooks = append(subbooks, *subbook)
	}

	return subbooks, nil
}

func (bc *bookContext) loadSubbook(subbookCode C.EB_Subbook_Code) (*Subbook, error) {
	if errEb := C.eb_set_subbook(bc.book, subbookCode); errEb != C.EB_SUCCESS {
		return nil, fmt.Errorf("eb_set_subbook failed with code: %s", formatError(errEb))
	}

	setSubbookContext(&subbookContext{
		blocksSeen:       make(map[uint32]bool),
		codepointsWide:   make(map[int]bool),
		codepointsNarrow: make(map[int]bool),
		flags:            bc.flags,
	})

	defer clearSubbookContext()

	var err error
	subbook := Subbook{
		GaijiWide:   make(map[int]Gaiji),
		GaijiNarrow: make(map[int]Gaiji),
	}

	if subbook.Title, err = bc.loadTitle(); err != nil {
		return nil, err
	}

	if subbook.Copyright, err = bc.loadCopyright(); err != nil {
		return nil, err
	}

	if errEb := C.eb_search_all_alphabet(bc.book); errEb == C.EB_SUCCESS {
		entries, err := bc.loadEntries()
		if err != nil {
			return nil, err
		}

		subbook.Entries = append(subbook.Entries, entries...)
	}

	if errEb := C.eb_search_all_kana(bc.book); errEb == C.EB_SUCCESS {
		entries, err := bc.loadEntries()
		if err != nil {
			return nil, err
		}

		subbook.Entries = append(subbook.Entries, entries...)
	}

	if errEb := C.eb_search_all_asis(bc.book); errEb == C.EB_SUCCESS {
		entries, err := bc.loadEntries()
		if err != nil {
			return nil, err
		}

		subbook.Entries = append(subbook.Entries, entries...)
	}

	var fonts []C.int
	if bc.flags&LoadFlagsGaiji16 != 0 {
		fonts = append(fonts, C.EB_FONT_16)
	}
	if bc.flags&LoadFlagsGaiji24 != 0 {
		fonts = append(fonts, C.EB_FONT_24)
	}
	if bc.flags&LoadFlagsGaiji30 != 0 {
		fonts = append(fonts, C.EB_FONT_30)
	}
	if bc.flags&LoadFlagsGaiji48 != 0 {
		fonts = append(fonts, C.EB_FONT_48)
	}

	setGaiji := func(codepoint int, glyph image.Image, mapping map[int]Gaiji) {
		gaiji, _ := mapping[codepoint]

		switch glyph.Bounds().Dy() {
		case 16:
			gaiji.Glyph16 = glyph
		case 24:
			gaiji.Glyph24 = glyph
		case 30:
			gaiji.Glyph30 = glyph
		case 48:
			gaiji.Glyph48 = glyph
		}

		mapping[codepoint] = gaiji
	}

	for _, font := range fonts {
		if errEb := C.eb_set_font(bc.book, font); errEb != C.EB_SUCCESS {
			return nil, fmt.Errorf("eb_set_font failed with code: %s", formatError(errEb))
		}

		var widthWide C.int
		if errEb := C.eb_wide_font_width(bc.book, &widthWide); errEb != C.EB_SUCCESS {
			return nil, fmt.Errorf("eb_wide_font_width failed with code: %s", formatError(errEb))
		}

		var widthNarrow C.int
		if errEb := C.eb_narrow_font_width(bc.book, &widthNarrow); errEb != C.EB_SUCCESS {
			return nil, fmt.Errorf("eb_narrow_font_width failed with code: %s", formatError(errEb))
		}

		var height C.int
		if errEb := C.eb_font_height(bc.book, &height); errEb != C.EB_SUCCESS {
			return nil, fmt.Errorf("eb_font_height failed with code: %s", formatError(errEb))
		}

		for codepoint := range activeSubbookContext.codepointsWide {
			glyph, err := bc.blitGaiji(codepoint, int(widthWide), int(height), fontTypeWide)
			if err != nil {
				return nil, err
			}

			setGaiji(codepoint, glyph, subbook.GaijiWide)
		}

		for codepoint := range activeSubbookContext.codepointsNarrow {
			glyph, err := bc.blitGaiji(codepoint, int(widthNarrow), int(height), fontTypeNarrow)
			if err != nil {
				return nil, err
			}

			setGaiji(codepoint, glyph, subbook.GaijiNarrow)
		}
	}

	return &subbook, nil
}

func (bc *bookContext) loadEntries() ([]Entry, error) {
	var entries []Entry

	for {
		var (
			hits     [256]C.EB_Hit
			hitCount C.int
		)

		if errEb := C.eb_hit_list(bc.book, C.int(len(hits)), &hits[0], &hitCount); errEb != C.EB_SUCCESS {
			return nil, fmt.Errorf("eb_hit_list failed with code: %s", formatError(errEb))
		}

		for _, hit := range hits[:hitCount] {
			var (
				entry Entry
				err   error
			)

			if entry.Heading, err = bc.loadContent(hit.heading, blockTypeHeading); err != nil {
				return nil, err
			}

			if entry.Text, err = bc.loadContent(hit.text, blockTypeText); err != nil {
				return nil, err
			}

			hasher := crc32.NewIEEE()
			hasher.Write([]byte(entry.Heading))
			hasher.Write([]byte(entry.Text))

			sum := hasher.Sum32()
			if seen, _ := activeSubbookContext.blocksSeen[sum]; !seen {
				entries = append(entries, entry)
				activeSubbookContext.blocksSeen[sum] = true
			}
		}

		if hitCount == 0 {
			return entries, nil
		}
	}
}

func (bc *bookContext) blitGaiji(codepoint, width, height int, font fontType) (image.Image, error) {
	bitmap := make([]C.char, width*height/8)

	switch font {
	case fontTypeWide:
		if errEb := C.eb_wide_font_character_bitmap(bc.book, C.int(codepoint), &bitmap[0]); errEb != C.EB_SUCCESS {
			return nil, fmt.Errorf("eb_wide_font_character_bitmap failed with code: %s", formatError(errEb))
		}
	case fontTypeNarrow:
		if errEb := C.eb_narrow_font_character_bitmap(bc.book, C.int(codepoint), &bitmap[0]); errEb != C.EB_SUCCESS {
			return nil, fmt.Errorf("eb_wide_font_character_bitmap failed with code: %s", formatError(errEb))
		}
	}

	glyph := image.NewGray(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var (
				byteOffset = y*width/8 + x/8
				bitOffset  = 7 - x%8
			)

			if b := byte(bitmap[byteOffset]); b&(1<<bitOffset) == 0 {
				glyph.Set(x, y, color.Gray{0xff})
			} else {
				glyph.Set(x, y, color.Gray{0x00})
			}
		}
	}

	return glyph, nil
}

func (bc *bookContext) loadTitle() (string, error) {
	var data [C.EB_MAX_TITLE_LENGTH + 1]C.char
	if errEb := C.eb_subbook_title(bc.book, &data[0]); errEb != C.EB_SUCCESS {
		return "", fmt.Errorf("eb_subbook_title failed with code: %s", formatError(errEb))
	}

	return bc.decoder.String(C.GoString(&data[0]))
}

func (bc *bookContext) loadCopyright() (string, error) {
	if C.eb_have_copyright(bc.book) == 0 {
		return "", nil
	}

	var position C.EB_Position
	if errEb := C.eb_copyright(bc.book, &position); errEb != C.EB_SUCCESS {
		return "", fmt.Errorf("eb_copyright failed with code: %s", formatError(errEb))
	}

	return bc.loadContent(position, blockTypeText)
}

func (bc *bookContext) loadContent(position C.EB_Position, blockType blockType) (string, error) {
	for {
		var (
			data     = (*C.char)(unsafe.Pointer(&bc.buffer[0]))
			dataSize = (C.size_t)(len(bc.buffer))
			dataUsed C.ssize_t
		)

		if errEb := C.eb_seek_text(bc.book, &position); errEb != C.EB_SUCCESS {
			return "", fmt.Errorf("eb_seek_text failed with code: %s", formatError(errEb))
		}

		switch blockType {
		case blockTypeHeading:
			if errEb := C.eb_read_heading(bc.book, nil, bc.hookset, nil, dataSize, data, &dataUsed); errEb != C.EB_SUCCESS {
				return "", fmt.Errorf("eb_read_heading failed with code: %s", formatError(errEb))
			}
		case blockTypeText:
			if errEb := C.eb_read_text(bc.book, nil, bc.hookset, nil, dataSize, data, &dataUsed); errEb != C.EB_SUCCESS {
				return "", fmt.Errorf("eb_read_text failed with code: %s", formatError(errEb))
			}
		default:
			panic("invalid block type")
		}

		if dataUsed+8 >= (C.ssize_t)(dataSize) {
			bc.buffer = make([]byte, dataSize*2)
		} else {
			return bc.decoder.String(C.GoString(data))
		}
	}
}

func Load(path string, flags LoadFlags) (*Book, error) {
	bc := bookContext{flags: flags}
	if err := bc.initialize(); err != nil {
		return nil, err
	}

	defer bc.shutdown()
	return bc.loadInternal(path)
}
