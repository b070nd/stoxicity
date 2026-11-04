package envfile

import (
	"bytes"
	"flag"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeFile writes content to path and then sets the file mode exactly, since
// the umask can change what WriteFile creates.
func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr string // substring the error must contain; "" means no error
		secret  string // text that must not appear in the error
	}{
		{
			name:  "pairs",
			input: "A=1\nB=two\n",
			want:  map[string]string{"A": "1", "B": "two"},
		},
		{
			name:  "blank lines and comments skipped",
			input: "\n# note\n   \nA=1\n",
			want:  map[string]string{"A": "1"},
		},
		{
			name:  "whitespace around key trimmed",
			input: "  KEY  = v\n",
			want:  map[string]string{"KEY": "v"},
		},
		{
			name:  "value keeps later equals signs",
			input: "A=b=c\n",
			want:  map[string]string{"A": "b=c"},
		},
		{
			name:  "double quotes stripped",
			input: `A="hello world"` + "\n",
			want:  map[string]string{"A": "hello world"},
		},
		{
			name:  "single quotes stripped",
			input: `A='x "y" z'` + "\n",
			want:  map[string]string{"A": `x "y" z`},
		},
		{
			name:  "quoted hash is not a comment",
			input: `A="a # b"` + "\n",
			want:  map[string]string{"A": "a # b"},
		},
		{
			name:  "quoted content kept as is",
			input: `A="  padded  "` + "\n",
			want:  map[string]string{"A": "  padded  "},
		},
		{
			name:  "trailing comment removed from unquoted value",
			input: "A=val # note\n",
			want:  map[string]string{"A": "val"},
		},
		{
			name:  "quoted value then comment",
			input: `A="v" # c` + "\n",
			want:  map[string]string{"A": "v"},
		},
		{
			name:  "hash without space is kept",
			input: "A=val#x\n",
			want:  map[string]string{"A": "val#x"},
		},
		{
			name:  "empty value",
			input: "A=\n",
			want:  map[string]string{"A": ""},
		},
		{
			name:  "empty quoted value",
			input: `A=""` + "\n",
			want:  map[string]string{"A": ""},
		},
		{
			name:  "CRLF line endings",
			input: "A=1\r\nB=2\r\n",
			want:  map[string]string{"A": "1", "B": "2"},
		},
		{
			name:  "later line replaces earlier key",
			input: "A=1\nA=2\n",
			want:  map[string]string{"A": "2"},
		},
		{
			name:    "missing equals sign",
			input:   "A=1\nNOPE\n",
			wantErr: "line 2",
			secret:  "NOPE",
		},
		{
			name:    "empty key",
			input:   "A=1\n=topsecretvalue\n",
			wantErr: "line 2",
			secret:  "topsecretvalue",
		},
		{
			name:    "invalid key characters",
			input:   "A=1\nBAD-KEY=topsecret\n",
			wantErr: "line 2",
			secret:  "topsecret",
		},
		{
			name:    "key starting with a digit",
			input:   "1A=x\n",
			wantErr: "line 1",
		},
		{
			name:    "export prefix refused",
			input:   "export A=1\n",
			wantErr: "line 1",
			secret:  "export",
		},
		{
			name:    "line number counts blank lines",
			input:   "\n\nA=1\n\nB\n",
			wantErr: "line 5",
			secret:  "B",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tt.input))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse(%q): no error, want one containing %q", tt.input, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Parse(%q): error %q does not contain %q", tt.input, err, tt.wantErr)
				}
				if tt.secret != "" && strings.Contains(err.Error(), tt.secret) {
					t.Errorf("Parse(%q): error %q contains line text", tt.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", tt.input, err)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("Parse(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSetup(t *testing.T) {
	tests := []struct {
		name    string
		real    map[string]string      // process environment before Setup
		files   map[string]string      // written into the project dir
		modes   map[string]os.FileMode // file modes; default 0600
		extra   []string               // extra files, relative to the project dir
		allow   []string               // extra allowed names
		want    map[string]string      // expected values; "" means unset or empty
		wantErr []string               // each must appear in the error
		notErr  []string               // must not appear in the error
	}{
		{
			name: "precedence .env, .env.local, then extras",
			files: map[string]string{
				".env":       "STOXICITY_A=dot\nSTOXICITY_B=dot\nSTOXICITY_C=dot\nSTOXICITY_D=dot\n",
				".env.local": "STOXICITY_B=local\nSTOXICITY_C=local\n",
				"a.env":      "STOXICITY_C=extra-a\nSTOXICITY_D=extra-a\n",
				"b.env":      "STOXICITY_D=extra-b\n",
			},
			extra: []string{"a.env", "b.env"},
			want: map[string]string{
				"STOXICITY_A": "dot",
				"STOXICITY_B": "local",
				"STOXICITY_C": "extra-a",
				"STOXICITY_D": "extra-b",
			},
		},
		{
			name:  "real environment wins",
			real:  map[string]string{"STOXICITY_A": "real"},
			files: map[string]string{".env": "STOXICITY_A=dot\n"},
			want:  map[string]string{"STOXICITY_A": "real"},
		},
		{
			name:  "empty real value does not win",
			real:  map[string]string{"STOXICITY_A": ""},
			files: map[string]string{".env": "STOXICITY_A=dot\n"},
			want:  map[string]string{"STOXICITY_A": "dot"},
		},
		{
			name: "empty value does not override",
			files: map[string]string{
				".env":       "STOXICITY_A=dot\n",
				".env.local": "STOXICITY_A=\nSTOXICITY_B=\n",
			},
			want: map[string]string{"STOXICITY_A": "dot", "STOXICITY_B": ""},
		},
		{
			name:    "0644 .env.local refused",
			files:   map[string]string{".env.local": "STOXICITY_K=v\n"},
			modes:   map[string]os.FileMode{".env.local": 0o644},
			wantErr: []string{"chmod 600", ".env.local"},
		},
		{
			name:    "0640 .env.local refused",
			files:   map[string]string{".env.local": "STOXICITY_K=v\n"},
			modes:   map[string]os.FileMode{".env.local": 0o640},
			wantErr: []string{"chmod 600", ".env.local"},
		},
		{
			name:  "0600 .env.local accepted",
			files: map[string]string{".env.local": "STOXICITY_K=v\n"},
			modes: map[string]os.FileMode{".env.local": 0o600},
			want:  map[string]string{"STOXICITY_K": "v"},
		},
		{
			name:    "0644 extra .local file refused",
			files:   map[string]string{"creds.local": "STOXICITY_K=v\n"},
			modes:   map[string]os.FileMode{"creds.local": 0o644},
			extra:   []string{"creds.local"},
			wantErr: []string{"chmod 600", "creds.local"},
		},
		{
			name:  "0644 .env is not checked",
			files: map[string]string{".env": "STOXICITY_A=1\n"},
			modes: map[string]os.FileMode{".env": 0o644},
			want:  map[string]string{"STOXICITY_A": "1"},
		},
		{
			name: "no files is fine",
			want: map[string]string{"STOXICITY_A": ""},
		},
		{
			name:    "missing extra file is an error",
			extra:   []string{"missing.env"},
			wantErr: []string{"missing.env"},
		},
		{
			name:  "allow list admits a named key",
			files: map[string]string{".env": "OTHER_KEY=ok\n"},
			allow: []string{"OTHER_KEY"},
			want:  map[string]string{"OTHER_KEY": "ok"},
		},
		{
			name:  "unknown key skipped",
			files: map[string]string{".env": "FOO=bar\nSTOXICITY_OK=yes\n"},
			want:  map[string]string{"FOO": "", "STOXICITY_OK": "yes"},
		},
		{
			name:  "quotes stripped",
			files: map[string]string{".env": "STOXICITY_Q1=\"a b\"\nSTOXICITY_Q2='c d'\n"},
			want:  map[string]string{"STOXICITY_Q1": "a b", "STOXICITY_Q2": "c d"},
		},
		{
			name:  "comments ignored",
			files: map[string]string{".env": "# STOXICITY_OFF=1\nSTOXICITY_ON=1 # yes\n"},
			want:  map[string]string{"STOXICITY_OFF": "", "STOXICITY_ON": "1"},
		},
		{
			name: "bad line names file and line, not value",
			files: map[string]string{
				".env.local": "STOXICITY_K=ok\nBAD LINE secret123\n",
			},
			wantErr: []string{"line 2", ".env.local"},
			notErr:  []string{"secret123"},
		},
		{
			name:    "export prefix refused with file and line",
			files:   map[string]string{".env": "export STOXICITY_E=1\n"},
			wantErr: []string{"line 1", ".env"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				mode := os.FileMode(0o600)
				if m, ok := tt.modes[name]; ok {
					mode = m
				}
				writeFile(t, filepath.Join(dir, name), content, mode)
			}
			for k := range tt.want {
				t.Setenv(k, "")
			}
			for k, v := range tt.real {
				t.Setenv(k, v)
			}
			var extra Files
			for _, name := range tt.extra {
				extra = append(extra, filepath.Join(dir, name))
			}

			err := Setup(extra, dir, tt.allow, nil)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Setup: unexpected error: %v", err)
				}
				for k, want := range tt.want {
					if got := os.Getenv(k); got != want {
						t.Errorf("%s = %q, want %q", k, got, want)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("Setup: no error, want one containing %q", tt.wantErr)
			}
			for _, s := range tt.wantErr {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("Setup: error %q does not contain %q", err, s)
				}
			}
			for _, s := range tt.notErr {
				if strings.Contains(err.Error(), s) {
					t.Errorf("Setup: error %q contains %q", err, s)
				}
			}
		})
	}
}

func TestSetupLogsNameOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), "SECRET_NAME=topsecretvalue\n", 0o600)
	t.Setenv("SECRET_NAME", "")

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	if err := Setup(nil, dir, nil, log); err != nil {
		t.Fatalf("Setup: unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "SECRET_NAME") {
		t.Errorf("warning does not name the variable: %q", buf.String())
	}
	if strings.Contains(buf.String(), "topsecretvalue") {
		t.Errorf("warning contains the value: %q", buf.String())
	}
	if got := os.Getenv("SECRET_NAME"); got != "" {
		t.Errorf("unaccepted variable was set to %q", got)
	}
}

func TestFilesSet(t *testing.T) {
	tests := []struct {
		name       string
		set        []string
		want       Files
		wantString string
	}{
		{
			name:       "none",
			want:       nil,
			wantString: "",
		},
		{
			name:       "appends in order",
			set:        []string{"a.env", "b.env"},
			want:       Files{"a.env", "b.env"},
			wantString: "a.env,b.env",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f Files
			for _, s := range tt.set {
				if err := f.Set(s); err != nil {
					t.Fatalf("Set(%q): unexpected error: %v", s, err)
				}
			}
			if !slices.Equal(f, tt.want) {
				t.Errorf("Files = %v, want %v", f, tt.want)
			}
			if got := f.String(); got != tt.wantString {
				t.Errorf("String() = %q, want %q", got, tt.wantString)
			}
		})
	}
}

func TestFilesFlag(t *testing.T) {
	var f Files
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Var(&f, "env-file", "extra env file")
	if err := fs.Parse([]string{"-env-file", "a.env", "-env-file", "b.env"}); err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if want := (Files{"a.env", "b.env"}); !slices.Equal(f, want) {
		t.Errorf("Files = %v, want %v", f, want)
	}
}
