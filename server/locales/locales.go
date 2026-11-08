// Package locales embeds the message templates of the server, one directory per
// language (tr first). Templates are text/template files.
package locales

import (
	"embed"
	"io/fs"
)

//go:embed tr/*.txt
var files embed.FS

// Template returns the template text of a language and name ("tr", "ihtarname").
func Template(lang, name string) (string, error) {
	b, err := fs.ReadFile(files, lang+"/"+name+".txt")
	return string(b), err
}
