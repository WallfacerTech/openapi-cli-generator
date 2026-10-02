package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/viper"
)

func TestFormatterJSONDoesNotEscapeHTML(t *testing.T) {
	var buf bytes.Buffer
	previous := Stdout
	Stdout = &buf
	defer func() { Stdout = previous }()

	viper.Set("output-format", "json")
	defer viper.Set("output-format", "")

	data := map[string]interface{}{"path": "R&D/Build", "body": "<body> & more"}
	if err := NewDefaultFormatter(false).Format(data); err != nil {
		t.Fatal(err)
	}

	want := "{\n  \"body\": \"<body> & more\",\n  \"path\": \"R&D/Build\"\n}\n\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
