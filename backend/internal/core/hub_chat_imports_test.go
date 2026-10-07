package core

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"os"
	"testing"
)

func fixtureChatZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, text := range files {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(text))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestPersonalChatZIPOriginalAndRead(t *testing.T) {
	s, owner, _, _, key := hubFixture(t)
	archive := fixtureChatZIP(t, map[string]string{"_chat.txt": "[9/19/26, 10:13:04 PM] Alice: Hello\nsecond line\n", "photo.jpg": "opaque media"})
	in := HubImportInput{ProviderID: "whatsapp-import", AccountID: "my-chat", DisplayName: "Personal chat", Filename: "chat.zip", Format: "whatsapp-zip", ContentBase64: base64.StdEncoding.EncodeToString(archive)}
	result, err := s.ImportHubSnapshot(owner, in, key)
	if err != nil {
		t.Fatal(err)
	}
	if result.Import.Bytes != len(archive) || result.Import.Live || result.Import.RecordCount != 1 {
		t.Fatal("ZIP provenance", result.Import)
	}
	root, err := s.hubImportRoot(false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	raw, err := readHubImportFile(root, result.Import.ID+".source", HubImportArchiveMaxBytes)
	if err != nil || !bytes.Equal(raw, archive) {
		t.Fatal("ZIP original changed")
	}
	secret, err := s.HubCredential(owner, result.Connection, key)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ExecuteHubImport(owner, result.Connection, secret, nil)
	if err != nil || len(page.Records) != 1 || page.Records[0].Text != "Hello\nsecond line" || page.Records[0].Fields["archive_chat_file"] != "_chat.txt" {
		t.Fatal("ZIP messages", page, err)
	}
	for _, archive := range [][]byte{fixtureChatZIP(t, map[string]string{"a.txt": "one", "b.txt": "two"}), fixtureChatZIP(t, map[string]string{"../chat.txt": "bad"}), fixtureChatZIP(t, map[string]string{"media.jpg": "no text"}), fixtureChatZIP(t, map[string]string{"chat.txt": "\xff\x00"}), []byte("not a ZIP")} {
		if _, _, _, err := parseHubImportSource("whatsapp-zip", archive); err == nil {
			t.Fatal("invalid ZIP accepted")
		}
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("_chat.txt")
	f.Write(bytes.Repeat([]byte("x"), HubImportMaxBytes+1))
	z.Close()
	if _, _, _, err := parseHubImportSource("whatsapp-zip", b.Bytes()); err == nil {
		t.Fatal("expanded text size ignored")
	}
	// A symlink entry must not be read or extracted.
	b.Reset()
	z = zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "_chat.txt", Method: zip.Deflate}
	h.SetMode(os.ModeSymlink | 0600)
	f, _ = z.CreateHeader(h)
	f.Write([]byte("/private"))
	z.Close()
	if _, _, _, err := parseHubImportSource("whatsapp-zip", b.Bytes()); err == nil {
		t.Fatal("ZIP symlink accepted")
	}
}
func TestTelegramDesktopAndPreparedWeChatImports(t *testing.T) {
	s, owner, _, _, key := hubFixture(t)
	telegram := `{"chats":{"list":[{"id":123,"name":"Personal","messages":[{"id":1,"type":"message","date":"2026-10-07T12:00:00","from":"Alice","text":["Hello ",{"type":"bold","text":"World"}]},{"id":2,"type":"service","date":"2026-10-07T12:01:00","actor":"Bob","text":""}]}]}}`
	wechat := "timestamp,sender,text,chat_id\r\n2026-10-07 12:00,Alice,\"Hello, World\nnext line\",chat-1\r\n"
	for _, tt := range []struct{ p, format, content, text string }{{"telegram-import", "telegram-json", telegram, "Hello World"}, {"wechat-import", "wechat-csv", wechat, "Hello, World\nnext line"}} {
		result, err := s.ImportHubSnapshot(owner, HubImportInput{ProviderID: tt.p, Format: tt.format, AccountID: "personal", DisplayName: "Personal", Filename: "chat.data", Content: tt.content}, key)
		if err != nil {
			t.Fatal(err)
		}
		secret, _ := s.HubCredential(owner, result.Connection, key)
		page, err := s.ExecuteHubImport(owner, result.Connection, secret, nil)
		if err != nil || len(page.Records) == 0 || page.Records[0].Text != tt.text || !page.Records[0].TimestampUncertain {
			t.Fatal(tt.p, page, err)
		}
	}
	for _, tt := range []struct{ format, content string }{{"telegram-json", `{"wrong":[]}`}, {"telegram-json", `{"messages":[{"id":1,"type":"message","text":[{"wrong":"x"}]}]}`}, {"wechat-csv", "timestamp,sender,text,text\n"}, {"wechat-csv", "sender,text\nAlice,Hi\n"}, {"wechat-csv", "timestamp,sender,text\nnow,Alice,unquoted,comma\n"}} {
		if _, _, _, err := parseHubImport(tt.format, tt.content); err == nil {
			t.Fatal("invalid chat format accepted", tt.format)
		}
	}
}
