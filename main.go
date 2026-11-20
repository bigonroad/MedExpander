//go:build windows

// TextExpander - a tiny standalone text expander for Windows.
// Type an abbreviation followed by SPACE and it is replaced by the full text.
// Abbreviations are read from snippets.txt (next to the .exe):
//
//	abbreviation <TAB> expansion        (use \n for a new line)
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

const appName = "MedExpand"

// ---------------------------------------------------------------- Win32 ----

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	msftedit = syscall.NewLazyDLL("msftedit.dll") // ships with Windows since XP; gives the RichEdit control used to bold "## " headers

	pSetWindowsHookEx         = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx           = user32.NewProc("CallNextHookEx")
	pUnhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	pGetAsyncKeyState         = user32.NewProc("GetAsyncKeyState")
	pGetKeyState              = user32.NewProc("GetKeyState")
	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pGetKeyboardLayout        = user32.NewProc("GetKeyboardLayout")
	pToUnicodeEx              = user32.NewProc("ToUnicodeEx")
	pSendInput                = user32.NewProc("SendInput")
	pRegisterClassEx          = user32.NewProc("RegisterClassExW")
	pCreateWindowEx           = user32.NewProc("CreateWindowExW")
	pDefWindowProc            = user32.NewProc("DefWindowProcW")
	pGetMessage               = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessage          = user32.NewProc("DispatchMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pPostMessage              = user32.NewProc("PostMessageW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pLoadIcon                 = user32.NewProc("LoadIconW")
	pCreateIcon               = user32.NewProc("CreateIcon")
	pCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	pAppendMenu               = user32.NewProc("AppendMenuW")
	pTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	pDestroyMenu              = user32.NewProc("DestroyMenu")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pRegisterWindowMessage    = user32.NewProc("RegisterWindowMessageW")
	pMessageBox               = user32.NewProc("MessageBoxW")
	pGetModuleHandle          = kernel32.NewProc("GetModuleHandleW")
	pCreateMutex              = kernel32.NewProc("CreateMutexW")
	pGetCurrentProcessId      = kernel32.NewProc("GetCurrentProcessId")
	pShellNotifyIcon          = shell32.NewProc("Shell_NotifyIconW")
	pShellExecute             = shell32.NewProc("ShellExecuteW")
	pSendMessage              = user32.NewProc("SendMessageW")
	pGetWindowTextLength      = user32.NewProc("GetWindowTextLengthW")
	pGetWindowText            = user32.NewProc("GetWindowTextW")
	pSetWindowText            = user32.NewProc("SetWindowTextW")
	pShowWindow               = user32.NewProc("ShowWindow")
	pSetFocus                 = user32.NewProc("SetFocus")
	pGetStockObject           = gdi32.NewProc("GetStockObject")
)

const (
	whKeyboardLL = 13
	whMouseLL    = 14

	wmNull        = 0x0000
	wmDestroy     = 0x0002
	wmKeyDown     = 0x0100
	wmSysKeyDown  = 0x0104
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmTray        = 0x0400 + 1

	llkhfInjected        = 0x10
	llkhfLowerILInjected = 0x02

	vkBack    = 0x08
	vkTab     = 0x09
	vkReturn  = 0x0D
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12
	vkCapital = 0x14
	vkEscape  = 0x1B
	vkSpace   = 0x20
	vkLeft    = 0x25
	vkInsert  = 0x2D
	vkDelete  = 0x2E
	vkLWin    = 0x5B
	vkRWin    = 0x5C
	vkNumLock = 0x90
	vkScroll  = 0x91
	vkLShift  = 0xA0
	vkRShift  = 0xA1
	vkLCtrl   = 0xA2
	vkRCtrl   = 0xA3
	vkLMenu   = 0xA4
	vkRMenu   = 0xA5

	inputKeyboard    = 1
	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4
	nifInfo    = 0x10

	mfString    = 0x0
	mfGrayed    = 0x1
	mfDisabled  = 0x2
	mfChecked   = 0x8
	mfSeparator = 0x800

	tpmRightButton = 0x2
	tpmReturnCmd   = 0x100

	idEdit       = 1
	idReload     = 2
	idPause      = 3
	idExit       = 4
	idStats      = 5
	idTypingTest = 6

	// ---- GUI window plumbing (editor / stats / typing-test windows) ----
	wmCommand       = 0x0111
	wmClose         = 0x0010
	wmSetFont       = 0x0030
	wmGetText       = 0x000D
	wmGetTextLength = 0x000E
	wmSetText       = 0x000C
	emSetLimitText  = 0x00C5
	enChange        = 0x0300

	wsChild       = 0x40000000
	wsVisible     = 0x10000000
	wsBorder      = 0x00800000
	wsVScroll     = 0x00200000
	esMultiline   = 0x0004
	esAutoVScroll = 0x0040
	esWantReturn  = 0x1000
	esReadOnly    = 0x0800

	// WS_CAPTION | WS_SYSMENU | WS_MINIMIZEBOX - a plain fixed-size window
	// (no resize grip, no maximize button, so the fixed layout always fits).
	wsFixedWindow = 0x00CA0000

	defaultGuiFont = 17 // stock object id for GetStockObject
	colorBtnFace   = 15 // for the window background brush (colorBtnFace+1)

	mbYesNo        = 0x00000004
	mbIconQuestion = 0x00000020
	idYes          = 6

	swShow = 5

	idEditorSave    = 101
	idEditorNotepad = 102
	idStatsRefresh  = 201
	idStatsReset    = 202
	idTypingInput   = 301
	idTypingFinish  = 302
	idTypingRetry   = 303

	idStatsSetRate = 204

	idDashEdit    = 401
	idDashReload  = 402
	idDashStats   = 403
	idDashTyping  = 404
	idDashPause   = 405
	idDashExit    = 406
	idDashRefresh = 407
	idDashClose   = 408

	// ---- RichEdit (used only by the snippet editor, so "## " headers can be bold) ----
	emSetTextMode   = 0x0459
	tmPlainText     = 1
	emSetCharFormat = 0x0444
	emExSetSel      = 0x0437
	emExGetSel      = 0x0436
	scfSelection    = 0x0001
	cfmBold         = 0x00000001
	cfeBold         = 0x0001

	wmLButtonDblClk = 0x0203
)

// CHARFORMATW, as defined by richedit.h - used with EM_SETCHARFORMAT to
// bold (or un-bold) a range of text in a RichEdit control.
type charFormatW struct {
	CbSize          uint32
	DwMask          uint32
	DwEffects       uint32
	YHeight         int32
	YOffset         int32
	CrTextColor     uint32
	BCharSet        byte
	BPitchAndFamily byte
	SzFaceName      [32]uint16
}

// CHARRANGE, used with EM_EXSETSEL/EM_EXGETSEL - character offsets here
// count the same way GetWindowText's string does (each "\r\n" is 2
// characters) because the control is put into TM_PLAINTEXT mode.
type charRange struct {
	CpMin int32
	CpMax int32
}

type kbdll struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// INPUT (keyboard variant) laid out for 64-bit Windows: 40 bytes.
type input struct {
	Type      uint32
	_         uint32
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
	_         [8]byte
}

type point struct{ X, Y int32 }

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type notifyIconData struct {
	Size            uint32
	HWnd            uintptr
	UID             uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Timeout         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

// ---------------------------------------------------------------- state ----

var (
	snippets       = map[string]string{}
	buf            []rune
	paused         bool
	hwnd           uintptr
	trayIcon       uintptr
	taskbarCreated uint32
	snippetPath    string
	hInst          uintptr

	hwndEditor        uintptr
	editorBox         uintptr
	editorStatus      uintptr
	richEditAvailable bool

	hwndStats      uintptr
	statsBox       uintptr
	statsRateInput uintptr

	hwndDashboard uintptr
	dashStatsBox  uintptr
	dashPauseBtn  uintptr

	hwndTyping        uintptr
	typingEditBox     uintptr
	typingResultLabel uintptr
	typingStart       time.Time
	typingStarted     bool
)

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func messageBox(text string, flags uintptr) {
	t, c := utf16Ptr(text), utf16Ptr(appName)
	pMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags)
	runtime.KeepAlive(t)
	runtime.KeepAlive(c)
}

func getWindowText(h uintptr) string {
	ln, _, _ := pGetWindowTextLength.Call(h)
	if ln == 0 {
		return ""
	}
	b := make([]uint16, ln+1)
	pGetWindowText.Call(h, uintptr(unsafe.Pointer(&b[0])), ln+1)
	return syscall.UTF16ToString(b)
}

func setWindowText(h uintptr, s string) {
	p := utf16Ptr(s)
	pSetWindowText.Call(h, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}

func setFont(h uintptr) {
	f, _, _ := pGetStockObject.Call(defaultGuiFont)
	pSendMessage.Call(h, wmSetFont, f, 1)
}

// createChild makes a standard EDIT/BUTTON/STATIC child control.
func createChild(class string, style uint32, x, y, w, h int32, parent uintptr, id uintptr, text string) uintptr {
	cls, t := utf16Ptr(class), utf16Ptr(text)
	ch, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(t)),
		uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h), parent, id, hInst, 0)
	runtime.KeepAlive(cls)
	runtime.KeepAlive(t)
	setFont(ch)
	return ch
}

func registerWindowClass(name string, proc uintptr) {
	cls := utf16Ptr(name)
	wc := wndClassEx{Instance: hInst, WndProc: proc, ClassName: cls, Background: colorBtnFace + 1}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	runtime.KeepAlive(cls)
}

// foregroundIsOwnProcess reports whether one of MedExpand's own windows
// (editor, stats, typing test) currently has focus. Expansion is skipped
// while that's true, so an abbreviation can't accidentally fire while
// you're editing snippets.txt or typing the speed-test paragraph.
func foregroundIsOwnProcess() bool {
	fg, _, _ := pGetForegroundWindow.Call()
	if fg == 0 {
		return false
	}
	var pid uint32
	pGetWindowThreadProcessId.Call(fg, uintptr(unsafe.Pointer(&pid)))
	own, _, _ := pGetCurrentProcessId.Call()
	return pid == uint32(own)
}

// ------------------------------------------------------- snippet loading ----

var cp1252 = [32]rune{
	0x20AC, 0x81, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8D, 0x017D, 0x8F,
	0x90, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x9D, 0x017E, 0x0178,
}

// decode copes with UTF-8, UTF-8 with BOM, UTF-16 (Excel "Unicode Text")
// and Windows-1252 (Excel "Text (Tab delimited)").
func decode(b []byte) string {
	if len(b) >= 2 && ((b[0] == 0xFF && b[1] == 0xFE) || (b[0] == 0xFE && b[1] == 0xFF)) {
		le := b[0] == 0xFF
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i+1])|uint16(b[i])<<8)
			}
		}
		return string(utf16.Decode(u))
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, 0, len(b))
	for _, c := range b {
		if c >= 0x80 && c <= 0x9F {
			r = append(r, cp1252[c-0x80])
		} else {
			r = append(r, rune(c))
		}
	}
	return string(r)
}

const sampleFile = "" +
	"# MedExpand snippets\r\n" +
	"# Each entry starts with a line '## abbreviation', followed by the text\r\n" +
	"# to type, up to the next '## ' line. Blank lines inside the text are fine\r\n" +
	"# (e.g. for a multi-paragraph letter) - only a line starting '## ' ends it.\r\n" +
	"#\r\n" +
	"# Three special placeholders are supported inside the text:\r\n" +
	"#   {#cursor}              leaves the cursor here instead of at the end\r\n" +
	"#   {#randomtext a|b|c}     picks one option at random, each time\r\n" +
	"#   {#insert othername}    inserts another snippet's text by its name\r\n" +
	"#\r\n" +
	"# A single space is added automatically after every expansion.\r\n" +
	"\r\n" +
	"## ccc\r\n" +
	"{#randomtext On auscultation, no murmurs, no crackles, no wheeze. Chest clear.|Chest clear, no wheeze, no murmurs, no crackles.}\r\n" +
	"\r\n" +
	"## rv\r\n" +
	"Review patient in {#cursor}, or come back earlier if feeling more unwell, or any other concerns.\r\n"

type snippetEntry struct {
	Name string
	Body string
}

// parseSnippetsOrdered reads the "## name" block format described in
// sampleFile, returning the leading comment block (preamble) separately
// from the entries themselves, in file order - needed so the editor can
// re-sort the entries alphabetically without disturbing the comments.
func parseSnippetsOrdered(text string) (preamble string, entries []snippetEntry) {
	lines := strings.Split(text, "\n")
	var pre []string
	seenHeader := false
	name := ""
	var body []string
	flush := func() {
		if name == "" {
			return
		}
		// trim leading/trailing blank lines but keep internal ones
		start, end := 0, len(body)
		for start < end && strings.TrimSpace(body[start]) == "" {
			start++
		}
		for end > start && strings.TrimSpace(body[end-1]) == "" {
			end--
		}
		entries = append(entries, snippetEntry{Name: name, Body: strings.Join(body[start:end], "\n")})
	}
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "## ") {
			flush()
			seenHeader = true
			name = strings.TrimSpace(line[3:])
			body = nil
			continue
		}
		if !seenHeader {
			pre = append(pre, line)
			continue
		}
		body = append(body, line)
	}
	flush()
	return strings.Join(pre, "\n"), entries
}

// parseSnippets is the map-returning form used everywhere expansion doesn't
// care about file order (matching, inserts, etc).
func parseSnippets(text string) map[string]string {
	_, entries := parseSnippetsOrdered(text)
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[e.Name] = e.Body
	}
	return m
}

// sortedSnippetsText re-renders raw snippets.txt content with its entries
// sorted alphabetically (case-insensitively) by abbreviation, keeping the
// leading comment block as-is. Used by the editor's Save button.
func sortedSnippetsText(raw string) string {
	preamble, entries := parseSnippetsOrdered(raw)
	sort.SliceStable(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	var sb strings.Builder
	if pre := strings.TrimRight(preamble, "\n"); strings.TrimSpace(pre) != "" {
		sb.WriteString(pre)
		sb.WriteString("\n\n")
	}
	for i, e := range entries {
		sb.WriteString("## ")
		sb.WriteString(e.Name)
		sb.WriteString("\n")
		sb.WriteString(e.Body)
		sb.WriteString("\n")
		if i != len(entries)-1 {
			sb.WriteString("\n")
		}
	}
	return toCRLF(sb.String())
}

func loadSnippets() (count int, err error) {
	data, err := os.ReadFile(snippetPath)
	if err != nil {
		return 0, err
	}
	snippets = parseSnippets(decode(data))
	buf = buf[:0]
	return len(snippets), nil
}

// resolveText expands {#cursor}, {#randomtext a|b|c} and {#insert name}
// placeholders inside body. It returns the final text and, if {#cursor}
// appeared (directly or inside an {#insert}), the rune offset into that
// text where the cursor should end up (-1 if there was none).
func resolveText(body string, depth int, visiting map[string]bool) (string, int) {
	if depth > 10 { // guard against {#insert} cycles
		return body, -1
	}
	var sb strings.Builder
	cursorPos := -1
	rest := body
	for {
		idx := strings.Index(rest, "{#")
		if idx < 0 {
			sb.WriteString(rest)
			break
		}
		sb.WriteString(rest[:idx])
		rest = rest[idx+2:]
		end := strings.IndexByte(rest, '}')
		if end < 0 { // unterminated - treat the rest as literal
			sb.WriteString("{#")
			sb.WriteString(rest)
			break
		}
		token := rest[:end]
		rest = rest[end+1:]
		trimmed := strings.TrimSpace(token)

		switch {
		case trimmed == "cursor":
			if cursorPos == -1 {
				cursorPos = utf8.RuneCountInString(sb.String())
			}
		case strings.HasPrefix(trimmed, "insert "):
			refName := strings.TrimSpace(trimmed[len("insert "):])
			if visiting[refName] {
				continue // already expanding this one further up - skip, don't hang
			}
			if refBody, ok := snippets[refName]; ok {
				visiting[refName] = true
				insText, insCursor := resolveText(refBody, depth+1, visiting)
				delete(visiting, refName)
				if insCursor != -1 && cursorPos == -1 {
					cursorPos = utf8.RuneCountInString(sb.String()) + insCursor
				}
				sb.WriteString(insText)
			}
		case strings.HasPrefix(trimmed, "randomtext "):
			opts := strings.Split(trimmed[len("randomtext "):], "|")
			if len(opts) > 0 {
				sb.WriteString(opts[rand.Intn(len(opts))])
			}
		default:
			// unrecognised placeholder - leave it exactly as typed
			sb.WriteString("{#")
			sb.WriteString(token)
			sb.WriteString("}")
		}
	}
	return sb.String(), cursorPos
}

// -------------------------------------------------------------- statistics ----

type statsData struct {
	TotalReplacements int            `json:"total_replacements"`
	TotalCharsSaved   int            `json:"total_chars_saved"`
	TypingCPM         float64        `json:"typing_characters_per_minute"`
	HourlyRate        float64        `json:"hourly_rate_gbp"`
	Counts            map[string]int `json:"counts_by_abbreviation"`
}

var (
	stats     = statsData{Counts: map[string]int{}}
	statsPath string
)

// Stats are kept in a plain CSV file (section,key,value rows) next to the
// exe, rather than JSON, so they can be opened directly in Excel or read by
// another program without any special parsing.
func loadStats() {
	if data, err := os.ReadFile(statsPath); err == nil {
		if s, ok := parseStatsCSV(decode(data)); ok {
			stats = s
			return
		}
	}
	migrateOldJSONStats() // one-off: pick up stats.json from an earlier MedExpand version, if present
}

func parseStatsCSV(text string) (statsData, bool) {
	r := csv.NewReader(strings.NewReader(text))
	records, err := r.ReadAll()
	if err != nil || len(records) == 0 {
		return statsData{}, false
	}
	s := statsData{Counts: map[string]int{}}
	for i, rec := range records {
		if i == 0 || len(rec) < 3 { // skip the header row
			continue
		}
		section, key, value := rec[0], rec[1], rec[2]
		switch section {
		case "summary":
			switch key {
			case "total_replacements":
				if n, err := strconv.Atoi(value); err == nil {
					s.TotalReplacements = n
				}
			case "total_chars_saved":
				if n, err := strconv.Atoi(value); err == nil {
					s.TotalCharsSaved = n
				}
			case "typing_characters_per_minute":
				if f, err := strconv.ParseFloat(value, 64); err == nil {
					s.TypingCPM = f
				}
			case "hourly_rate_gbp":
				if f, err := strconv.ParseFloat(value, 64); err == nil {
					s.HourlyRate = f
				}
			}
		case "count":
			if n, err := strconv.Atoi(value); err == nil {
				s.Counts[key] = n
			}
		}
	}
	return s, true
}

func migrateOldJSONStats() {
	oldPath := strings.TrimSuffix(statsPath, filepath.Ext(statsPath)) + ".json"
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return
	}
	var s statsData
	if json.Unmarshal(data, &s) != nil {
		return
	}
	if s.Counts == nil {
		s.Counts = map[string]int{}
	}
	stats = s
	saveStats() // write it straight back out as stats.csv so it isn't lost
}

func saveStats() {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write([]string{"section", "key", "value"})
	w.Write([]string{"summary", "total_replacements", strconv.Itoa(stats.TotalReplacements)})
	w.Write([]string{"summary", "total_chars_saved", strconv.Itoa(stats.TotalCharsSaved)})
	w.Write([]string{"summary", "typing_characters_per_minute", strconv.FormatFloat(stats.TypingCPM, 'f', 2, 64)})
	w.Write([]string{"summary", "hourly_rate_gbp", strconv.FormatFloat(stats.HourlyRate, 'f', 2, 64)})

	names := make([]string, 0, len(stats.Counts))
	for k := range stats.Counts {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		w.Write([]string{"count", k, strconv.Itoa(stats.Counts[k])})
	}
	w.Flush()
	os.WriteFile(statsPath, buf.Bytes(), 0644)
}

// recordExpansion logs one successful expansion: which abbreviation, and
// roughly how many characters typing it out in full would have cost.
func recordExpansion(abbr, text string) {
	if stats.Counts == nil {
		stats.Counts = map[string]int{}
	}
	stats.Counts[abbr]++
	stats.TotalReplacements++
	if saved := utf8.RuneCountInString(text) - utf8.RuneCountInString(abbr); saved > 0 {
		stats.TotalCharsSaved += saved
	}
	saveStats()
}

func formatDuration(minutes float64) string {
	total := int(minutes*60 + 0.5)
	h, m, s := total/3600, (total%3600)/60, total%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

const defaultCPM = 200 // ~40 WPM, used until the typing test has measured the real speed

func formatStats() string {
	cpm := stats.TypingCPM
	note := "measured from your typing speed test"
	if cpm <= 0 {
		cpm = defaultCPM
		note = "a default estimate - run the typing speed test to use your real speed"
	}
	minutesSaved := float64(stats.TotalCharsSaved) / cpm

	var sb strings.Builder
	fmt.Fprintf(&sb, "MedExpand statistics\r\n\r\n")
	fmt.Fprintf(&sb, "Total replacements: %d\r\n", stats.TotalReplacements)
	fmt.Fprintf(&sb, "Estimated time saved: %s\r\n", formatDuration(minutesSaved))
	fmt.Fprintf(&sb, "  (based on %.0f characters/minute - %s)\r\n\r\n", cpm, note)

	if stats.HourlyRate > 0 {
		cost := (minutesSaved / 60) * stats.HourlyRate
		fmt.Fprintf(&sb, "Estimated cost saved: £%.2f (at £%.2f/hour)\r\n\r\n", cost, stats.HourlyRate)
	} else {
		fmt.Fprintf(&sb, "Cost saved: set your hourly rate below to see this.\r\n\r\n")
	}

	type kv struct {
		name  string
		count int
	}
	list := make([]kv, 0, len(stats.Counts))
	for k, v := range stats.Counts {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].name < list[j].name
	})

	fmt.Fprintf(&sb, "Most used snippets:\r\n")
	if len(list) == 0 {
		fmt.Fprintf(&sb, "  (none yet)\r\n")
	}
	max := 15
	if len(list) < max {
		max = len(list)
	}
	for i := 0; i < max; i++ {
		fmt.Fprintf(&sb, "  %2d. %-20s %d times\r\n", i+1, list[i].name, list[i].count)
	}
	return sb.String()
}

// ----------------------------------------------------------- keyboard hook ----

func isDown(vk uintptr) bool {
	r, _, _ := pGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

func isToggled(vk uintptr) bool {
	r, _, _ := pGetKeyState.Call(vk)
	return r&1 != 0
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// match looks for an abbreviation at the end of the typed buffer, resolves
// its placeholders, and returns the text to type plus where the cursor
// should land in it (-1 = leave it at the end, after the trailing space).
func match(b []rune) (abbr, text string, cursorPos int, ok bool) {
	for i := 0; i < len(b); i++ {
		if i > 0 && isWordRune(b[i-1]) {
			continue
		}
		if body, found := snippets[string(b[i:])]; found {
			resolved, cursor := resolveText(body, 0, map[string]bool{})
			return string(b[i:]), resolved, cursor, true
		}
	}
	return "", "", -1, false
}

func newKey(vk, scan uint16, flags uint32) input {
	return input{Type: inputKeyboard, Vk: vk, Scan: scan, Flags: flags}
}

// expand backspaces the typed abbreviation, types text (plus one trailing
// space), then - if cursorPos is set - presses Left enough times to put the
// caret back at that position instead of leaving it at the very end.
func expand(abbr, text string, cursorPos int) {
	var in []input
	for range []rune(abbr) {
		in = append(in, newKey(vkBack, 0, 0), newKey(vkBack, 0, keyeventfKeyUp))
	}

	full := text + " "
	totalRunes := 0
	for _, r := range full {
		totalRunes++
		switch r {
		case '\r':
		case '\n':
			in = append(in, newKey(vkReturn, 0, 0), newKey(vkReturn, 0, keyeventfKeyUp))
		case '\t':
			in = append(in, newKey(vkTab, 0, 0), newKey(vkTab, 0, keyeventfKeyUp))
		default:
			for _, u := range utf16.Encode([]rune{r}) {
				in = append(in,
					newKey(0, u, keyeventfUnicode),
					newKey(0, u, keyeventfUnicode|keyeventfKeyUp))
			}
		}
	}

	if cursorPos >= 0 && cursorPos < totalRunes {
		for i := 0; i < totalRunes-cursorPos; i++ {
			in = append(in, newKey(vkLeft, 0, 0), newKey(vkLeft, 0, keyeventfKeyUp))
		}
	}

	const chunk = 100
	for i := 0; i < len(in); i += chunk {
		end := i + chunk
		if end > len(in) {
			end = len(in)
		}
		pSendInput.Call(uintptr(end-i), uintptr(unsafe.Pointer(&in[i])), unsafe.Sizeof(input{}))
	}
}

func handleKey(k *kbdll) (block bool) {
	vk := k.VkCode

	switch vk {
	case vkShift, vkLShift, vkRShift, vkControl, vkLCtrl, vkRCtrl,
		vkMenu, vkLMenu, vkRMenu, vkCapital, vkNumLock, vkScroll:
		return false // modifier on its own: ignore
	}

	ctrl, alt := isDown(vkControl), isDown(vkMenu)
	if vk == vkLWin || vk == vkRWin || isDown(vkLWin) || isDown(vkRWin) || ctrl != alt {
		buf = buf[:0] // keyboard shortcut, not typing
		return false
	}

	switch {
	case vk == vkBack:
		if len(buf) > 0 {
			buf = buf[:len(buf)-1]
		}
		return false
	case vk == vkSpace:
		if abbr, text, cursorPos, ok := match(buf); ok {
			buf = buf[:0]
			recordExpansion(abbr, text)
			expand(abbr, text, cursorPos)
			return true // swallow the space; the expansion supplies its own
		}
		buf = buf[:0]
		return false
	case vk == vkReturn, vk == vkTab, vk == vkEscape, vk == vkInsert, vk == vkDelete,
		vk >= 0x21 && vk <= 0x28, // page up/down, end, home, arrows
		vk >= 0x70 && vk <= 0x87: // function keys
		buf = buf[:0]
		return false
	}

	var state [256]byte
	if isDown(vkShift) {
		state[vkShift] = 0x80
	}
	if ctrl {
		state[vkControl] = 0x80
	}
	if alt {
		state[vkMenu] = 0x80
	}
	if isToggled(vkCapital) {
		state[vkCapital] = 1
	}
	if isToggled(vkNumLock) {
		state[vkNumLock] = 1
	}

	fg, _, _ := pGetForegroundWindow.Call()
	tid, _, _ := pGetWindowThreadProcessId.Call(fg, 0)
	hkl, _, _ := pGetKeyboardLayout.Call(tid)

	var out [8]uint16
	n, _, _ := pToUnicodeEx.Call(uintptr(vk), uintptr(k.ScanCode),
		uintptr(unsafe.Pointer(&state[0])), uintptr(unsafe.Pointer(&out[0])),
		uintptr(len(out)), 4 /* don't disturb dead-key state */, hkl)
	if cnt := int32(n); cnt >= 1 {
		for _, r := range utf16.Decode(out[:cnt]) {
			if r >= 0x20 {
				buf = append(buf, r)
			} else {
				buf = buf[:0]
			}
		}
		if len(buf) > 64 {
			buf = append(buf[:0], buf[len(buf)-64:]...)
		}
	}
	return false
}

func keyboardProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) >= 0 && (wParam == wmKeyDown || wParam == wmSysKeyDown) {
		if foregroundIsOwnProcess() {
			buf = buf[:0] // typing in our own editor/stats/typing-test window: never expand there
		} else if !paused {
			k := (*kbdll)(unsafe.Pointer(lParam))
			if k.Flags&(llkhfInjected|llkhfLowerILInjected) == 0 {
				if handleKey(k) {
					return 1
				}
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

func mouseProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) >= 0 {
		switch wParam {
		case wmLButtonDown, wmRButtonDown, wmMButtonDown:
			buf = buf[:0] // clicking elsewhere = new place in the text
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

// ------------------------------------------------------------- tray icon ----

func copyUTF16(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-1]
	}
	copy(dst, u)
}

func trayNotify(op uintptr, balloonTitle, balloonText string) {
	var n notifyIconData
	n.Size = uint32(unsafe.Sizeof(n))
	n.HWnd = hwnd
	n.UID = 1
	n.Flags = nifMessage | nifIcon | nifTip
	n.CallbackMessage = wmTray
	n.Icon = trayIcon
	tip := fmt.Sprintf("%s - %d snippets", appName, len(snippets))
	if paused {
		tip += " (paused)"
	}
	copyUTF16(n.Tip[:], tip)
	if balloonText != "" {
		n.Flags |= nifInfo
		copyUTF16(n.Info[:], balloonText)
		copyUTF16(n.InfoTitle[:], balloonTitle)
		n.InfoFlags = 1
	}
	pShellNotifyIcon.Call(op, uintptr(unsafe.Pointer(&n)))
}

// makeIcon draws a 32x32 tray icon: a white "M" inside a filled 5-pointed
// star. It's built pixel-by-pixel (no image library available cross-compiled
// without CGO) using a point-in-polygon test for the star and a
// distance-to-line-segment test for the M's four strokes.
func makeIcon() uintptr {
	const sz = 32
	xor := make([]byte, sz*sz*4)
	and := make([]byte, sz*sz/8)

	type pt struct{ x, y float64 }
	starPts := func(cx, cy, rOuter, rInner float64) []pt {
		pts := make([]pt, 0, 10)
		for i := 0; i < 10; i++ {
			angle := -math.Pi/2 + float64(i)*math.Pi/5
			r := rOuter
			if i%2 == 1 {
				r = rInner
			}
			pts = append(pts, pt{cx + r*math.Cos(angle), cy + r*math.Sin(angle)})
		}
		return pts
	}
	// rInner/rOuter is fatter than a "classic" star (~0.38) so the M has
	// enough room in the middle without its corners poking past the points.
	poly := starPts(15.5, 15.5, 13.5, 8.2)
	inStar := func(x, y float64) bool {
		inside := false
		n := len(poly)
		j := n - 1
		for i := 0; i < n; i++ {
			xi, yi := poly[i].x, poly[i].y
			xj, yj := poly[j].x, poly[j].y
			if ((yi > y) != (yj > y)) && (x < (xj-xi)*(y-yi)/(yj-yi)+xi) {
				inside = !inside
			}
			j = i
		}
		return inside
	}
	distToSeg := func(px, py, x1, y1, x2, y2 float64) float64 {
		dx, dy := x2-x1, y2-y1
		l2 := dx*dx + dy*dy
		if l2 == 0 {
			return math.Hypot(px-x1, py-y1)
		}
		t := ((px-x1)*dx + (py-y1)*dy) / l2
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
		return math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
	}
	mx0, my0, mx1, my1 := 10.5, 11.5, 21.5, 21.5
	midX, midY := (mx0+mx1)/2, my0+(my1-my0)*0.5 // the M's middle valley
	strokes := [][4]float64{
		{mx0, my0, mx0, my1},   // left leg
		{mx0, my0, midX, midY}, // left diagonal
		{midX, midY, mx1, my0}, // right diagonal
		{mx1, my0, mx1, my1},   // right leg
	}
	const strokeWidth = 2.3

	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			o := (y*sz + x) * 4
			if !inStar(fx, fy) {
				and[y*4+x/8] |= 0x80 >> (x % 8) // transparent outside the star
				continue
			}
			isM := false
			for _, s := range strokes {
				if distToSeg(fx, fy, s[0], s[1], s[2], s[3]) <= strokeWidth/2 {
					isM = true
					break
				}
			}
			b, g, r := byte(0xD9), byte(0x6C), byte(0x2B) // brand blue, star fill
			if isM {
				b, g, r = 0xFF, 0xFF, 0xFF // white "M"
			}
			xor[o], xor[o+1], xor[o+2], xor[o+3] = b, g, r, 0xFF
		}
	}
	h, _, _ := pCreateIcon.Call(0, sz, sz, 1, 32,
		uintptr(unsafe.Pointer(&and[0])), uintptr(unsafe.Pointer(&xor[0])))
	if h == 0 {
		h, _, _ = pLoadIcon.Call(0, 32512) // IDI_APPLICATION
	}
	return h
}

func addMenu(m uintptr, flags, id uintptr, text string) {
	p := utf16Ptr(text)
	pAppendMenu.Call(m, flags, id, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}

func reload(quiet bool) {
	n, err := loadSnippets()
	if err != nil {
		messageBox("Could not read snippets.txt:\n"+snippetPath+"\n\n"+err.Error(), 0x10)
		return
	}
	if quiet {
		trayNotify(nimModify, "", "")
	} else {
		trayNotify(nimModify, appName, fmt.Sprintf("%d snippets loaded", n))
	}
}

func showMenu() {
	m, _, _ := pCreatePopupMenu.Call()
	addMenu(m, mfString|mfGrayed|mfDisabled, 0, fmt.Sprintf("%s (%d snippets)", appName, len(snippets)))
	addMenu(m, mfSeparator, 0, "")
	addMenu(m, mfString, idEdit, "Edit snippets...")
	addMenu(m, mfString, idReload, "Reload snippets")
	addMenu(m, mfSeparator, 0, "")
	addMenu(m, mfString, idStats, "Statistics...")
	addMenu(m, mfString, idTypingTest, "Typing speed test...")
	addMenu(m, mfSeparator, 0, "")
	f := uintptr(mfString)
	if paused {
		f |= mfChecked
	}
	addMenu(m, f, idPause, "Pause")
	addMenu(m, mfSeparator, 0, "")
	addMenu(m, mfString, idExit, "Exit")

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, tpmReturnCmd|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	pPostMessage.Call(hwnd, wmNull, 0, 0)
	pDestroyMenu.Call(m)

	switch cmd {
	case idEdit:
		openEditorWindow()
	case idReload:
		reload(false)
	case idStats:
		openStatsWindow()
	case idTypingTest:
		openTypingWindow()
	case idPause:
		paused = !paused
		buf = buf[:0]
		trayNotify(nimModify, "", "")
		updateDashboard()
	case idExit:
		pDestroyWindow.Call(hwnd)
	}
}

func wndProc(h, m, wp, lp uintptr) uintptr {
	switch uint32(m) {
	case wmTray:
		switch uint32(lp) {
		case wmRButtonUp:
			showMenu() // right-click: the full context menu
		case wmLButtonUp, wmLButtonDblClk:
			openDashboardWindow() // left-click or double-click: the dashboard
		}
		return 0
	case wmDestroy:
		trayNotify(nimDelete, "", "")
		pPostQuitMessage.Call(0)
		return 0
	}
	if taskbarCreated != 0 && uint32(m) == taskbarCreated {
		trayNotify(nimAdd, "", "") // Explorer restarted: put the icon back
		return 0
	}
	r, _, _ := pDefWindowProc.Call(h, m, wp, lp)
	return r
}

// ------------------------------------------------------------ edit window ----

// toCRLF makes sure line breaks are "\r\n" before handing text to a plain
// Win32 Edit control, which (unlike most text editors) only recognises CRLF
// as a line break - a bare "\n" is displayed as nothing, which is why a file
// saved with Unix-style line endings looks like one giant unreadable line.
func toCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// setBold bolds (or un-bolds) the character range [start,end) in a RichEdit
// control, without disturbing where the caret currently is.
func setBold(h uintptr, start, end int32, bold bool) {
	var saved charRange
	pSendMessage.Call(h, emExGetSel, 0, uintptr(unsafe.Pointer(&saved)))

	rng := charRange{CpMin: start, CpMax: end}
	pSendMessage.Call(h, emExSetSel, 0, uintptr(unsafe.Pointer(&rng)))

	cf := charFormatW{DwMask: cfmBold}
	cf.CbSize = uint32(unsafe.Sizeof(cf))
	if bold {
		cf.DwEffects = cfeBold
	}
	pSendMessage.Call(h, emSetCharFormat, scfSelection, uintptr(unsafe.Pointer(&cf)))

	pSendMessage.Call(h, emExSetSel, 0, uintptr(unsafe.Pointer(&saved))) // restore the caret
}

// reformatHeaders makes every "## name" header line bold and leaves
// everything else normal weight. It's called after loading text into the
// snippet editor, after Save re-sorts it, and on every edit inside it.
func reformatHeaders(h uintptr) {
	if !richEditAvailable {
		return
	}
	text := getWindowText(h)
	lines := strings.Split(text, "\r\n")
	var pos int32
	for _, line := range lines {
		// Rune count, not byte count: character offsets here are UTF-16
		// code units, and every character in this file is within the Basic
		// Multilingual Plane, so 1 rune = 1 UTF-16 unit.
		lineLen := int32(utf8.RuneCountInString(line))
		setBold(h, pos, pos+lineLen, strings.HasPrefix(line, "## "))
		pos += lineLen + 2 // +2 for the "\r\n" that was split off
	}
}

const editorHelpText = "Format: a line '## name' starts a new snippet - everything below it, " +
	"up to the next '## ' line, is what gets typed (blank lines inside are fine). " +
	"Placeholders: {#cursor} leaves your cursor there  |  {#randomtext a|b|c} picks one at random  |  {#insert name} inserts another snippet."

func openEditorWindow() {
	if hwndEditor != 0 {
		pSetForegroundWindow.Call(hwndEditor)
		return
	}
	const w, h = 680, 640
	cls, title := utf16Ptr("MedExpandEditor"), utf16Ptr("MedExpand - Edit snippets")
	hwndEditor, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		uintptr(wsFixedWindow|wsVisible), 120, 40, w, h, 0, 0, hInst, 0)
	runtime.KeepAlive(cls)
	runtime.KeepAlive(title)

	createChild("STATIC", wsChild|wsVisible, 15, 15, w-50, 65, hwndEditor, 0, editorHelpText)

	boxClass := "EDIT"
	if richEditAvailable {
		boxClass = "RICHEDIT50W"
	}
	editorBox = createChild(boxClass, wsChild|wsVisible|wsBorder|wsVScroll|esMultiline|esAutoVScroll|esWantReturn,
		15, 90, w-50, h-195, hwndEditor, 1, "")
	if richEditAvailable {
		// Must be set before any text goes in: makes \r\n counting in
		// EM_EXSETSEL/EM_EXGETSEL match plain GetWindowText, which is what
		// reformatHeaders relies on.
		pSendMessage.Call(editorBox, emSetTextMode, tmPlainText, 0)
	}
	pSendMessage.Call(editorBox, emSetLimitText, 0, 0) // raise the 64KB default cap

	if data, err := os.ReadFile(snippetPath); err == nil {
		setWindowText(editorBox, toCRLF(decode(data)))
		reformatHeaders(editorBox)
	}

	createChild("BUTTON", wsChild|wsVisible, 15, h-95, 110, 32, hwndEditor, idEditorSave, "Save")
	createChild("BUTTON", wsChild|wsVisible, 135, h-95, 170, 32, hwndEditor, idEditorNotepad, "Open in Notepad")
	editorStatus = createChild("STATIC", wsChild|wsVisible, 320, h-88, w-350, 20, hwndEditor, 0, "")

	pShowWindow.Call(hwndEditor, swShow)
	pSetFocus.Call(editorBox)
}

func editorWndProc(h, m, wp, lp uintptr) uintptr {
	switch uint32(m) {
	case wmCommand:
		id, notify := uint16(wp), uint16(wp>>16)
		switch id {
		case 1: // the snippet-text box itself
			if notify == enChange {
				reformatHeaders(editorBox)
			}
		case idEditorSave:
			sorted := sortedSnippetsText(getWindowText(editorBox))
			if err := os.WriteFile(snippetPath, []byte(sorted), 0644); err != nil {
				setWindowText(editorStatus, "Could not save: "+err.Error())
			} else {
				setWindowText(editorBox, sorted) // reflect the new alphabetical order right away
				reformatHeaders(editorBox)
				if n, err2 := loadSnippets(); err2 != nil {
					setWindowText(editorStatus, "Saved, but could not reload: "+err2.Error())
				} else {
					setWindowText(editorStatus, fmt.Sprintf("Saved and sorted A-Z - %d snippets loaded.", n))
					trayNotify(nimModify, "", "")
				}
			}
		case idEditorNotepad:
			verb, exe, arg := utf16Ptr("open"), utf16Ptr("notepad.exe"), utf16Ptr(`"`+snippetPath+`"`)
			pShellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(exe)),
				uintptr(unsafe.Pointer(arg)), 0, 1)
			runtime.KeepAlive(verb)
			runtime.KeepAlive(exe)
			runtime.KeepAlive(arg)
		}
		return 0
	case wmClose:
		pDestroyWindow.Call(h)
		return 0
	case wmDestroy:
		hwndEditor = 0
		return 0
	}
	r, _, _ := pDefWindowProc.Call(h, m, wp, lp)
	return r
}

// ----------------------------------------------------------- stats window ----

func openStatsWindow() {
	if hwndStats != 0 {
		updateStatsBox()
		pSetForegroundWindow.Call(hwndStats)
		return
	}
	const w, h = 520, 500
	cls, title := utf16Ptr("MedExpandStats"), utf16Ptr("MedExpand - Statistics")
	hwndStats, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		uintptr(wsFixedWindow|wsVisible), 150, 80, w, h, 0, 0, hInst, 0)
	runtime.KeepAlive(cls)
	runtime.KeepAlive(title)

	statsBox = createChild("EDIT", wsChild|wsVisible|wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly,
		15, 15, w-50, h-155, hwndStats, 1, "")

	createChild("STATIC", wsChild|wsVisible, 15, h-130, 140, 24, hwndStats, 0, "Hourly rate (£):")
	statsRateInput = createChild("EDIT", wsChild|wsVisible|wsBorder, 165, h-133, 90, 24, hwndStats, 0, formatRate(stats.HourlyRate))
	createChild("BUTTON", wsChild|wsVisible, 265, h-135, 100, 28, hwndStats, idStatsSetRate, "Set rate")

	createChild("BUTTON", wsChild|wsVisible, 15, h-85, 110, 32, hwndStats, idStatsRefresh, "Refresh")
	createChild("BUTTON", wsChild|wsVisible, 135, h-85, 140, 32, hwndStats, idStatsReset, "Reset stats")

	updateStatsBox()
	pShowWindow.Call(hwndStats, swShow)
}

func formatRate(rate float64) string {
	if rate <= 0 {
		return ""
	}
	return strconv.FormatFloat(rate, 'f', 2, 64)
}

func updateStatsBox() {
	if statsBox != 0 {
		setWindowText(statsBox, formatStats())
	}
}

func statsWndProc(h, m, wp, lp uintptr) uintptr {
	switch uint32(m) {
	case wmCommand:
		switch uint16(wp) {
		case idStatsRefresh:
			updateStatsBox()
		case idStatsSetRate:
			txt := strings.TrimSpace(strings.ReplaceAll(getWindowText(statsRateInput), "£", ""))
			if txt == "" {
				stats.HourlyRate = 0
				saveStats()
				updateStatsBox()
			} else if rate, err := strconv.ParseFloat(txt, 64); err == nil && rate >= 0 {
				stats.HourlyRate = rate
				saveStats()
				updateStatsBox()
				setWindowText(statsRateInput, formatRate(rate))
			} else {
				prompt, title := utf16Ptr("Enter a valid hourly rate, e.g. 45 or 45.50 (or leave blank to clear it)."), utf16Ptr(appName)
				pMessageBox.Call(h, uintptr(unsafe.Pointer(prompt)), uintptr(unsafe.Pointer(title)), 0x30)
				runtime.KeepAlive(prompt)
				runtime.KeepAlive(title)
			}
		case idStatsReset:
			prompt, title := utf16Ptr("Reset all statistics? This cannot be undone."), utf16Ptr(appName)
			r, _, _ := pMessageBox.Call(h, uintptr(unsafe.Pointer(prompt)), uintptr(unsafe.Pointer(title)), mbYesNo|mbIconQuestion)
			runtime.KeepAlive(prompt)
			runtime.KeepAlive(title)
			if r == idYes {
				stats = statsData{Counts: map[string]int{}}
				saveStats()
				updateStatsBox()
			}
		}
		return 0
	case wmClose:
		pDestroyWindow.Call(h)
		return 0
	case wmDestroy:
		hwndStats, statsBox, statsRateInput = 0, 0, 0
		return 0
	}
	r, _, _ := pDefWindowProc.Call(h, m, wp, lp)
	return r
}

// ------------------------------------------------------ typing test window ----

const typingParagraph = "The quick brown fox jumps over the lazy dog while the sun sets slowly " +
	"behind the distant hills. Good typing speed comes with practice, patience, and a steady " +
	"rhythm. Take a breath, relax your shoulders, and let your fingers move naturally across the keys."

func openTypingWindow() {
	if hwndTyping != 0 {
		pSetForegroundWindow.Call(hwndTyping)
		return
	}
	const w, h = 560, 480
	cls, title := utf16Ptr("MedExpandTypingTest"), utf16Ptr("MedExpand - Typing speed test")
	hwndTyping, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		uintptr(wsFixedWindow|wsVisible), 180, 100, w, h, 0, 0, hInst, 0)
	runtime.KeepAlive(cls)
	runtime.KeepAlive(title)

	createChild("STATIC", wsChild|wsVisible, 15, 15, w-50, 24, hwndTyping,
		0, "Type the paragraph below as quickly and accurately as you can, then click Finish:")
	createChild("STATIC", wsChild|wsVisible|wsBorder, 15, 45, w-50, 110, hwndTyping, 0, typingParagraph)

	typingEditBox = createChild("EDIT", wsChild|wsVisible|wsBorder|esMultiline|esAutoVScroll|esWantReturn,
		15, 165, w-50, 130, hwndTyping, idTypingInput, "")
	createChild("BUTTON", wsChild|wsVisible, 15, 305, 110, 32, hwndTyping, idTypingFinish, "Finish")
	createChild("BUTTON", wsChild|wsVisible, 135, 305, 120, 32, hwndTyping, idTypingRetry, "Try again")

	typingResultLabel = createChild("STATIC", wsChild|wsVisible, 15, 345, w-50, 90, hwndTyping, 0, "")

	typingStarted = false
	pShowWindow.Call(hwndTyping, swShow)
	pSetFocus.Call(typingEditBox)
}

func typingWndProc(h, m, wp, lp uintptr) uintptr {
	switch uint32(m) {
	case wmCommand:
		id, notify := uint16(wp), uint16(wp>>16)
		switch id {
		case idTypingInput:
			if notify == enChange && !typingStarted {
				if getWindowText(typingEditBox) != "" {
					typingStarted = true
					typingStart = time.Now()
				}
			}
		case idTypingFinish:
			if !typingStarted {
				setWindowText(typingResultLabel, "Start typing the paragraph first, then click Finish.")
			} else {
				elapsed := time.Since(typingStart)
				chars := utf8.RuneCountInString(getWindowText(typingEditBox))
				minutes := elapsed.Minutes()
				if minutes < 1.0/60 {
					minutes = 1.0 / 60 // guard against a near-instant click
				}
				cpm := float64(chars) / minutes
				stats.TypingCPM = cpm
				saveStats()
				setWindowText(typingResultLabel, fmt.Sprintf(
					"%d characters in %.1fs  =  %.0f WPM (%.0f characters/min).\r\nSaved as your typing speed - the Statistics window will now use this for its time-saved estimate.",
					chars, elapsed.Seconds(), cpm/5, cpm))
			}
		case idTypingRetry:
			typingStarted = false
			setWindowText(typingEditBox, "")
			setWindowText(typingResultLabel, "")
			pSetFocus.Call(typingEditBox)
		}
		return 0
	case wmClose:
		pDestroyWindow.Call(h)
		return 0
	case wmDestroy:
		hwndTyping, typingStarted = 0, false
		return 0
	}
	r, _, _ := pDefWindowProc.Call(h, m, wp, lp)
	return r
}

// ------------------------------------------------------------- dashboard ----
//
// Opened by left-clicking or double-clicking the tray icon (right-click
// still gives the normal context menu). It's a single window with a button
// for every menu function, plus the statistics shown inline.

func pauseLabel() string {
	if paused {
		return "Resume"
	}
	return "Pause"
}

func openDashboardWindow() {
	if hwndDashboard != 0 {
		updateDashboard()
		pSetForegroundWindow.Call(hwndDashboard)
		return
	}
	const w, h = 540, 620
	cls, title := utf16Ptr("MedExpandDashboard"), utf16Ptr(appName)
	hwndDashboard, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		uintptr(wsFixedWindow|wsVisible), 160, 60, w, h, 0, 0, hInst, 0)
	runtime.KeepAlive(cls)
	runtime.KeepAlive(title)

	createChild("BUTTON", wsChild|wsVisible, 15, 15, 160, 32, hwndDashboard, idDashEdit, "Edit snippets...")
	createChild("BUTTON", wsChild|wsVisible, 185, 15, 160, 32, hwndDashboard, idDashReload, "Reload snippets")
	createChild("BUTTON", wsChild|wsVisible, 15, 55, 160, 32, hwndDashboard, idDashStats, "Statistics...")
	createChild("BUTTON", wsChild|wsVisible, 185, 55, 160, 32, hwndDashboard, idDashTyping, "Typing speed test...")
	dashPauseBtn = createChild("BUTTON", wsChild|wsVisible, 15, 95, 160, 32, hwndDashboard, idDashPause, pauseLabel())
	createChild("BUTTON", wsChild|wsVisible, 185, 95, 160, 32, hwndDashboard, idDashExit, "Exit MedExpand")

	createChild("STATIC", wsChild|wsVisible, 15, 140, 200, 20, hwndDashboard, 0, "Statistics:")
	dashStatsBox = createChild("EDIT", wsChild|wsVisible|wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly,
		15, 165, w-50, h-250, hwndDashboard, 0, "")
	createChild("BUTTON", wsChild|wsVisible, 15, h-75, 140, 32, hwndDashboard, idDashRefresh, "Refresh stats")
	createChild("BUTTON", wsChild|wsVisible, 170, h-75, 110, 32, hwndDashboard, idDashClose, "Close")

	updateDashboard()
	pShowWindow.Call(hwndDashboard, swShow)
}

func updateDashboard() {
	if dashStatsBox != 0 {
		setWindowText(dashStatsBox, formatStats())
	}
	if dashPauseBtn != 0 {
		setWindowText(dashPauseBtn, pauseLabel())
	}
}

func dashboardWndProc(h, m, wp, lp uintptr) uintptr {
	switch uint32(m) {
	case wmCommand:
		switch uint16(wp) {
		case idDashEdit:
			openEditorWindow()
		case idDashReload:
			reload(false)
			updateDashboard()
		case idDashStats:
			openStatsWindow()
		case idDashTyping:
			openTypingWindow()
		case idDashPause:
			paused = !paused
			buf = buf[:0]
			trayNotify(nimModify, "", "")
			updateDashboard()
		case idDashExit:
			pDestroyWindow.Call(hwnd) // destroys the hidden main window -> full app exit
		case idDashRefresh:
			updateDashboard()
		case idDashClose:
			pDestroyWindow.Call(h)
		}
		return 0
	case wmClose:
		pDestroyWindow.Call(h)
		return 0
	case wmDestroy:
		hwndDashboard, dashStatsBox, dashPauseBtn = 0, 0, 0
		return 0
	}
	r, _, _ := pDefWindowProc.Call(h, m, wp, lp)
	return r
}

// ------------------------------------------------------------------ main ----

func main() {
	runtime.LockOSThread()

	name := utf16Ptr("MedExpandSingleInstanceMutex")
	_, _, lastErr := pCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if e, ok := lastErr.(syscall.Errno); ok && e == 183 { // already running
		return
	}

	exe, _ := os.Executable()
	snippetPath = filepath.Join(filepath.Dir(exe), "snippets.txt")
	if _, err := os.Stat(snippetPath); os.IsNotExist(err) {
		os.WriteFile(snippetPath, []byte(sampleFile), 0644)
	}
	loaded, loadErr := loadSnippets()

	statsPath = filepath.Join(filepath.Dir(exe), "stats.csv")
	loadStats()

	richEditAvailable = msftedit.Load() == nil // falls back to a plain edit box (no bold headers) if this ever fails

	hInst, _, _ = pGetModuleHandle.Call(0)
	trayIcon = makeIcon()

	className := utf16Ptr("MedExpandWindow")
	wc := wndClassEx{
		Instance:  hInst,
		WndProc:   syscall.NewCallback(wndProc),
		ClassName: className,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, hInst, 0)
	if hwnd == 0 {
		messageBox("Could not create the tray window.", 0x10)
		return
	}

	registerWindowClass("MedExpandEditor", syscall.NewCallback(editorWndProc))
	registerWindowClass("MedExpandStats", syscall.NewCallback(statsWndProc))
	registerWindowClass("MedExpandTypingTest", syscall.NewCallback(typingWndProc))
	registerWindowClass("MedExpandDashboard", syscall.NewCallback(dashboardWndProc))
	tb := utf16Ptr("TaskbarCreated")
	r, _, _ := pRegisterWindowMessage.Call(uintptr(unsafe.Pointer(tb)))
	taskbarCreated = uint32(r)

	if loadErr != nil {
		trayNotify(nimAdd, "", "")
		messageBox("Could not read snippets.txt:\n"+snippetPath+"\n\n"+loadErr.Error(), 0x10)
	} else {
		trayNotify(nimAdd, appName, fmt.Sprintf("Running - %d snippets loaded", loaded))
	}

	kh, _, _ := pSetWindowsHookEx.Call(whKeyboardLL, syscall.NewCallback(keyboardProc), hInst, 0)
	mh, _, _ := pSetWindowsHookEx.Call(whMouseLL, syscall.NewCallback(mouseProc), hInst, 0)
	if kh == 0 {
		messageBox("Could not install the keyboard hook.", 0x10)
		return
	}

	var m msg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}

	pUnhookWindowsHookEx.Call(kh)
	if mh != 0 {
		pUnhookWindowsHookEx.Call(mh)
	}
}
