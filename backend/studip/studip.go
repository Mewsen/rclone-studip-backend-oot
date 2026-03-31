package studip

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/lib/encoder"
)

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "Stud.IP",
		Prefix:      "studip",
		Description: "Stud.IP",
		NewFs:       NewFs,
		Options: []fs.Option{{
			Name:     "base_url",
			Help:     "Base URL of the Stud.IP JSON API v1 endpoint",
			Default:  "https://elearning.uni-bremen.de/jsonapi.php/v1/",
			Required: true,
		}, {
			Name:     "username",
			Help:     "Stud.IP username used for login",
			Required: true,
		}, {
			Name:       "password",
			Help:       "Stud.IP password used for login",
			IsPassword: true,
			Required:   true,
		}, {
			Name:     "course_id",
			Help:     "Stud.IP course ID (e.g. 59e88658b39093836455413bd1f24f29)",
			Required: true,
		}, {
			Name:     "license",
			Help:     "License ID applied to uploaded files",
			Required: true,
			Default:  "UNDEF_LICENSE",
			Examples: fs.OptionExamples{
				fs.OptionExample{
					Value: "FREE_LICENSE",
					Help:  "Works that have been published under a free license, i.e. the distribution and usually also modification of which is permitted without license costs, may be made available for teaching without restrictions. \n\nTypical examples are:\n- Open Access publications \n- Open Educational Resources (OER) \n- Works under Creative Commons licenses (e.g. Wikipedia content) \n\nAttention: Make sure on a case-by-case basis what restrictions on distribution and modification the respective license may contain.",
				},
				fs.OptionExample{
					Value: "SELFMADE_NONPUB",
					Help:  "Self-authored, unpublished work",
				},
				fs.OptionExample{
					Value: "NON_TEXTUAL",
					Help:  "Copyright-protected and published works",
				},
				fs.OptionExample{
					Value: "TEXT_NO_LICENSE",
					Help:  "Published texts without an acquired license or separate permission",
				},
				fs.OptionExample{
					Value: "WITH_LICENSE",
					Help:  "Permission of use or license exists",
				},
				fs.OptionExample{
					Value: "UNDEF_LICENSE",
					Help:  "Unclear License",
				},
			},
		}, {
			Name:     config.ConfigEncoding,
			Help:     config.ConfigEncodingHelp,
			Advanced: true,
			Default: (encoder.Base |
				encoder.EncodeLeftSpace |
				encoder.EncodeRightSpace |
				encoder.EncodeCrLf |
				encoder.EncodeLeftCrLfHtVt |
				encoder.EncodeRightCrLfHtVt |
				encoder.EncodeInvalidUtf8),
		},
		},
	})
}

type Options struct {
	BaseURL  string               `config:"base_url"`
	Username string               `config:"username"`
	Password string               `config:"password"`
	CourseID string               `config:"course_id"`
	License  string               `config:"license"`
	Enc      encoder.MultiEncoder `config:"encoding"`
}

var (
	_ fs.Fs        = &Fs{}
	_ fs.Object    = &Object{}
	_ fs.Purger    = &Fs{}
	_ fs.Mover     = &Fs{}
	_ fs.DirMover  = &Fs{}
	_ fs.MimeTyper = &Object{}
	_ fs.Directory = &Directory{}
)

func Assert(cond bool, msg string) {
	if cond {
		return
	}

	if msg == "" {
		msg = "condition is false"
	}

	pc, file, line, ok := runtime.Caller(1)
	if !ok {
		panic("assert failed: " + msg)
	}

	fnName := "<unknown>"
	if fn := runtime.FuncForPC(pc); fn != nil {
		fnName = fn.Name()
	}

	panic(fmt.Sprintf(
		"assert failed at %s:%d (%s): %s",
		filepath.Base(file),
		line,
		fnName,
		msg,
	))
}
