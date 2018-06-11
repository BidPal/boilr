package template

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"text/template"

	"github.com/BidPal/boilr/pkg/boilr"
	"github.com/BidPal/boilr/pkg/prompt"
	"github.com/BidPal/boilr/pkg/util/osutil"
	"github.com/BidPal/boilr/pkg/util/stringutil"
	"github.com/BidPal/boilr/pkg/util/tlog"
)

// Interface is contains the behavior of boilr templates.
type Interface interface {
	// Executes the template on the given target directory path.
	Execute(string) error

	// If used, the template will execute using default values.
	UseDefaultValues()

	// Returns the metadata of the template.
	Info() Metadata
}

func (t dirTemplate) Info() Metadata {
	return t.Metadata
}

// Get retrieves the template from a path.
func Get(path string) (Interface, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	ctxt, err := readContext(filepath.Join(absPath, boilr.ContextFileName))
	if err != nil {
		return nil, err
	}

	metadataExists, err := osutil.FileExists(filepath.Join(absPath, boilr.TemplateMetadataName))
	if err != nil {
		return nil, err
	}

	md, err := func() (Metadata, error) {
		if !metadataExists {
			return Metadata{}, nil
		}

		b, err := ioutil.ReadFile(filepath.Join(absPath, boilr.TemplateMetadataName))
		if err != nil {
			return Metadata{}, err
		}

		var m Metadata
		if err := json.Unmarshal(b, &m); err != nil {
			return Metadata{}, err
		}

		return m, nil
	}()

	return &dirTemplate{
		Context:  ctxt,
		FuncMap:  FuncMap,
		Path:     filepath.Join(absPath, boilr.TemplateDirName),
		Metadata: md,
	}, err
}

type dirTemplate struct {
	Path     string
	Context  map[string]interface{}
	FuncMap  template.FuncMap
	Metadata Metadata

	alignment         string
	ShouldUseDefaults bool
}

func (t *dirTemplate) UseDefaultValues() {
	t.ShouldUseDefaults = true
}

func (t *dirTemplate) BindPrompts() {
	t.bindPromptsCore(t.Context, func() interface{} { return true })
}

func (t *dirTemplate) bindPromptsCore(context map[string]interface{}, gateFn func() interface{}) {
	for key, val := range context {
		if m, ok := val.(map[string]interface{}); ok {
			t.bindPromptsCore(m, prompt.New(key, false))
		} else {
			t.bindPrompt(context, key, gateFn)
		}
	}
}

func (t *dirTemplate) bindPrompt(context map[string]interface{}, key string, gateFn func() interface{}) {
	val := context[key]
	def := val
	if a, ok := def.([]interface{}); ok {
		def = a[0]
	}

	p := prompt.New(key, val)
	t.FuncMap[key] = func() interface{} {
		v := def
		if !t.ShouldUseDefaults {
			if gateFn().(bool) {
				v = p()
			}
		}

		context[key] = v
		return v
	}
}

// Execute fills the template with the project metadata.
func (t *dirTemplate) Execute(dirPrefix string) error {
	localContext := filepath.Join(dirPrefix, boilr.LocalDefaultsFileName)
	if err := t.updateContext(localContext); err != nil {
		return err
	}

	t.BindPrompts()

	isOnlyWhitespace := func(buf []byte) bool {
		wsre := regexp.MustCompile(`\S`)

		return !wsre.Match(buf)
	}

	// TODO create io.ReadWriter from string
	// TODO refactor name manipulation
	if err := filepath.Walk(t.Path, func(filename string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Path relative to the root of the template directory
		oldName, err := filepath.Rel(t.Path, filename)
		if err != nil {
			return err
		}

		buf := stringutil.NewString("")

		// TODO translate errors into meaningful ones
		fnameTmpl := template.Must(t.delims(template.
			New("file name template").
			Option(Options...).
			Funcs(FuncMap)).
			Parse(oldName))

		if err := fnameTmpl.Execute(buf, nil); err != nil {
			return err
		}

		newName := buf.String()

		target := filepath.Join(dirPrefix, newName)

		if info.IsDir() {
			if err := os.Mkdir(target, 0755); err != nil {
				if !os.IsExist(err) {
					return err
				}
			}
		} else {
			fi, err := os.Lstat(filename)
			if err != nil {
				return err
			}

			// Delete target file if it exists
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}

			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, fi.Mode())
			if err != nil {
				return err
			}
			defer f.Close()

			defer func(fname string) {
				contents, err := ioutil.ReadFile(fname)
				if err != nil {
					tlog.Debug(fmt.Sprintf("couldn't read the contents of file %q, got error %q", fname, err))
					return
				}

				if isOnlyWhitespace(contents) {
					os.Remove(fname)
					return
				}
			}(f.Name())

			contentsTmpl := template.Must(t.delims(template.
				New("file contents template").
				Option(Options...).
				Funcs(FuncMap)).
				ParseFiles(filename))

			fileTemplateName := filepath.Base(filename)

			if err := contentsTmpl.ExecuteTemplate(f, fileTemplateName, nil); err != nil {
				return err
			}

			if !t.ShouldUseDefaults {
				tlog.Success(fmt.Sprintf("Created %s", newName))
			}
		}

		return nil
	}); err != nil {
		return err
	}

	return t.saveContext(localContext)
}

// updateContext will augment the current context with the values from the file at the provided path
func (t *dirTemplate) updateContext(path string) error {
	context, err := readContext(path)
	if err != nil {
		return err
	}

	if context != nil {
		for key, value := range context {
			if key == boilr.DelimsProperty {
				continue
			}

			// If the value exists in the local file but not in the actual context, we'll just ignore it as most likely the template has changed since it was last saved
			if _, ok := t.Context[key]; ok {
				t.Context[key] = value
			}
		}
	}

	return nil
}

// saveContext will save the current context to the file at the provided path
func (t *dirTemplate) saveContext(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(t.Context); err != nil {
		return err
	}

	return nil
}

func (t *dirTemplate) delims(tt *template.Template) *template.Template {
	if d, ok := t.Context[boilr.DelimsProperty].([]interface{}); ok {
		if len(d) == 2 {
			left, lok := d[0].(string)
			right, rok := d[1].(string)
			if lok && rok {
				tt.Delims(left, right)
			}
		}
	}
	return tt
}

func readContext(fname string) (map[string]interface{}, error) {
	f, err := os.Open(fname)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}
	defer f.Close()

	var metadata map[string]interface{}
	dec := json.NewDecoder(f)
	if err := dec.Decode(&metadata); err != nil {
		return nil, err
	}

	return metadata, nil
}
