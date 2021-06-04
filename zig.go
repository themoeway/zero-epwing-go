package zig

import (
	"fmt"
	"hash/crc32"
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

var (
	activeQuery     *queryContext
	activeQueryLock sync.Mutex
)

func setActiveQuery(query *queryContext) {
	activeQueryLock.Lock()
	activeQuery = query
}

func clearActiveQuery() {
	activeQuery = nil
	activeQueryLock.Unlock()
}

type queryContext struct {
	blocksSeen  map[uint32]bool
	gaijiWide   map[int]bool
	gaijiNarrow map[int]bool
}

//export hookCallback
func hookCallback(book *C.EB_Book, appendix *C.EB_Appendix, container *C.void, hookCode C.EB_Hook_Code, argc C.int, argv *C.uint) C.EB_Error_Code {
	var marker string
	switch hookCode {
	case C.EB_HOOK_NARROW_FONT:
		activeQuery.gaijiNarrow[int(*argv)] = true
		marker = fmt.Sprintf("{{n_%d}}", *argv)
	case C.EB_HOOK_WIDE_FONT:
		activeQuery.gaijiWide[int(*argv)] = true
		marker = fmt.Sprintf("{{w_%d}}", *argv)
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

type BookEntry struct {
	Heading string
	Text    string
}

type BookSubbook struct {
	Title       string
	Copyright   string
	Entries     []BookEntry
	GaijiWide   []int
	GaijiNarrow []int
}

type Book struct {
	DiscCode string
	CharCode string
	Subbooks []BookSubbook
}

type bookContext struct {
	buffer  []byte
	decoder *encoding.Decoder
	hookset *C.EB_Hookset
	book    *C.EB_Book
}

func (c *bookContext) initialize() error {
	if errEb := C.eb_initialize_library(); errEb != C.EB_SUCCESS {
		return fmt.Errorf("eb_initialize_library failed with code: %s", formatError(errEb))
	}

	c.book = (*C.EB_Book)(C.calloc(1, C.size_t(unsafe.Sizeof(C.EB_Book{}))))
	C.eb_initialize_book(c.book)

	c.hookset = (*C.EB_Hookset)(C.calloc(1, C.size_t(unsafe.Sizeof(C.EB_Hookset{}))))
	C.eb_initialize_hookset(c.hookset)

	if err := c.installHooks(); err != nil {
		return err
	}

	c.buffer = make([]byte, 22)
	c.decoder = japanese.EUCJP.NewDecoder()

	return nil
}

func (c *bookContext) shutdown() {
	C.eb_finalize_hookset(c.hookset)
	C.free(unsafe.Pointer(c.hookset))

	C.eb_finalize_book(c.book)
	C.free(unsafe.Pointer(c.book))

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

func (bc *bookContext) loadSubbooks() ([]BookSubbook, error) {
	var (
		subbookCodes [C.EB_MAX_SUBBOOKS]C.EB_Subbook_Code
		subbookCount C.int
	)

	if errEb := C.eb_subbook_list(bc.book, &subbookCodes[0], &subbookCount); errEb != C.EB_SUCCESS {
		return nil, fmt.Errorf("eb_subbook_list failed with code: %s", formatError(errEb))
	}

	var subbooks []BookSubbook
	for i := 0; i < int(subbookCount); i++ {
		subbook, err := bc.loadSubbook(subbookCodes[i])
		if err != nil {
			return nil, err
		}

		subbooks = append(subbooks, *subbook)
	}

	return subbooks, nil
}

func (bc *bookContext) loadSubbook(subbookCode C.EB_Subbook_Code) (*BookSubbook, error) {
	if errEb := C.eb_set_subbook(bc.book, subbookCode); errEb != C.EB_SUCCESS {
		return nil, fmt.Errorf("eb_set_subbook failed with code: %s", formatError(errEb))
	}

	var (
		subbook BookSubbook
		err     error
	)

	if subbook.Title, err = bc.loadTitle(); err != nil {
		return nil, err
	}

	if subbook.Copyright, err = bc.loadCopyright(); err != nil {
		return nil, err
	}

	{
		query := &queryContext{
			blocksSeen:  make(map[uint32]bool),
			gaijiWide:   make(map[int]bool),
			gaijiNarrow: make(map[int]bool),
		}

		setActiveQuery(query)

		if errEb := C.eb_search_all_alphabet(bc.book); errEb == C.EB_SUCCESS {
			entries, err := bc.loadEntries(query)
			if err != nil {
				return nil, err
			}

			subbook.Entries = append(subbook.Entries, entries...)
		}

		if errEb := C.eb_search_all_kana(bc.book); errEb == C.EB_SUCCESS {
			entries, err := bc.loadEntries(query)
			if err != nil {
				return nil, err
			}

			subbook.Entries = append(subbook.Entries, entries...)
		}

		if errEb := C.eb_search_all_asis(bc.book); errEb == C.EB_SUCCESS {
			entries, err := bc.loadEntries(query)
			if err != nil {
				return nil, err
			}

			subbook.Entries = append(subbook.Entries, entries...)
		}

		clearActiveQuery()

		for gaiji := range query.gaijiWide {
			subbook.GaijiWide = append(subbook.GaijiWide, gaiji)
		}

		for gaiji := range query.gaijiNarrow {
			subbook.GaijiNarrow = append(subbook.GaijiNarrow, gaiji)
		}
	}

	return &subbook, nil
}

func (bc *bookContext) loadEntries(query *queryContext) ([]BookEntry, error) {
	var entries []BookEntry

	for {
		var (
			hits     [256]C.EB_Hit
			hitCount C.int
		)

		if errEb := C.eb_hit_list(bc.book, (C.int)(len(hits)), &hits[0], &hitCount); errEb != C.EB_SUCCESS {
			return nil, fmt.Errorf("eb_hit_list failed with code: %s", formatError(errEb))
		}

		for _, hit := range hits[:hitCount] {
			var (
				entry BookEntry
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
			if seen, _ := query.blocksSeen[sum]; !seen {
				entries = append(entries, entry)
				query.blocksSeen[sum] = true
			}
		}

		if hitCount == 0 {
			return entries, nil
		}
	}
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

func Load(path string) (*Book, error) {
	var bc bookContext
	if err := bc.initialize(); err != nil {
		return nil, err
	}

	defer bc.shutdown()
	return bc.loadInternal(path)
}
