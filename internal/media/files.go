package media

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// fileTypes: the files people may send, by extension, with the content
// types phones report for them. Anything else is refused: in particular
// apps and programs (APK, EXE, scripts), a common way to spread malware
// ("install this to see my photo"), and nothing scans files for viruses.
var fileTypes = map[string][]string{
	"pdf":  {"application/pdf"},
	"doc":  {"application/msword"},
	"docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	"xls":  {"application/vnd.ms-excel"},
	"xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	"ppt":  {"application/vnd.ms-powerpoint"},
	"pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	"odt":  {"application/vnd.oasis.opendocument.text"},
	"ods":  {"application/vnd.oasis.opendocument.spreadsheet"},
	"odp":  {"application/vnd.oasis.opendocument.presentation"},
	"rtf":  {"application/rtf", "text/rtf"},
	"txt":  {"text/plain"},
	"csv":  {"text/csv", "text/comma-separated-values"},
	"md":   {"text/markdown", "text/plain"},
	"epub": {"application/epub+zip"},
	"zip":  {"application/zip", "application/x-zip-compressed"},
	"7z":   {"application/x-7z-compressed"},
	"rar":  {"application/vnd.rar", "application/x-rar-compressed"},
	"mp3":  {"audio/mpeg"},
	"m4a":  {"audio/mp4", "audio/x-m4a"},
	"ogg":  {"audio/ogg"},
	"opus": {"audio/ogg", "audio/opus"},
	"wav":  {"audio/wav", "audio/x-wav"},
	"aac":  {"audio/aac"},
}

const maxFileName = 200

// cleanFileName returns a safe display and download name, and its
// extension; ok is false if the name is unusable or the type isn't allowed.
func cleanFileName(name, contentType string) (string, bool) {
	// keep only the last path element, without control or direction-
	// changing characters (which can disguise "fdp.exe" as "exe.pdf")
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '"' || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > maxFileName {
		return "", false
	}
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || dot == len(name)-1 {
		return "", false
	}
	ext := strings.ToLower(name[dot+1:])
	for _, ct := range fileTypes[ext] {
		if ct == contentType {
			return name, true
		}
	}
	return "", false
}
