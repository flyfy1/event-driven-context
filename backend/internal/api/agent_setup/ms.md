# Sambungkan agent pengekodan ke Event-driven Context

## Projek sasaran

`{{.Project}}`

{{if .HasProject}}ID projek dinyatakan di atas. Gunakannya untuk setiap panggilan projek walaupun dokumen dimuat turun tanpa URL asal. Panduan ini tidak membuktikan projek wujud atau pengguna mempunyai akses; sahkan keahlian selepas OAuth.{{else}}Belum ada projek dipilih. Tanya pengguna projek sedia ada yang hendak digunakan sebelum mengkonfigurasi atau membaca konteks. PROJECT_ID di bawah hanyalah ruang letak. Jangan cipta projek secara automatik.{{end}}

## Matlamat

Sambungkan direktori kerja kepada akaun sedia ada melalui MCP jauh, pasang Skill pencatat rasmi, kemudian sahkan projek tepat dengan bacaan sebenar. Fail konfigurasi sahaja bukan bukti.

- Titik akhir MCP: `{{.MCPURL}}`
- Skill pencatat: `{{.SkillURL}}`

Jangan minta pengguna menampal token akses dalam perbualan. Gunakan OAuth dalam pelayar. Semak konfigurasi sedia ada, kekalkan entri lain dan jangan aktifkan hook rakaman automatik tanpa permintaan berasingan.

## 1. Konfigurasi MCP jauh

Pilih klien yang menjalankan tugas ini. Jika entri event-context sudah ada, semak alamatnya dan kekalkan tetapan lain; jangan ganti entri yang menunjuk ke perkhidmatan lain secara senyap.

### Codex

Semak dahulu. Tambah pelayan hanya jika entri belum wujud:

```sh
codex mcp get event-context
codex mcp add event-context \
  --url {{.MCPURL}} \
  --oauth-resource {{.MCPURL}}
```

Lengkapkan OAuth dengan kedua-dua scope. Buka halaman kebenaran jika perlu dan tunggu arahan mengesahkan log masuk berjaya:

```sh
codex mcp login event-context --scopes context:read,context:write
```

### Claude Code

Semak dahulu. Tambah pelayan hanya jika entri belum wujud:

```sh
claude mcp get event-context
claude mcp add --transport http --scope local \
  event-context {{.MCPURL}}
```

Buka /mcp dalam Claude Code, pilih event-context dan lengkapkan OAuth dalam pelayar. Gunakan penemuan sumber terlindung dan pendaftaran dinamik; jangan masukkan token statik atau rahsia klien.

## 2. Pasang Skill pencatat

Baca dan muat turun Skill asal daripada URL di atas. Simpan dalam projek semasa mengikut laluan klien:

- Codex: `.agents/skills/edc-recorder/SKILL.md`
- Claude Code: `.claude/skills/edc-recorder/SKILL.md`

Bandingkan fail sedia ada sebelum menggantikannya dan kekalkan suntingan pengguna. Jangan commit kelayakan OAuth atau keadaan peribadi klien.

## 3. Sahkan sambungan sebenar

Sambung semula MCP atau buka sesi agent baharu supaya pelayan dan Skill dimuatkan. Panggil list_projects dan sahkan ID projek sasaran wujud. Kemudian panggil query_events dengan:

```json
{
  "project_id": "{{.Project}}",
  "limit": 5
}
```

Jika tersedia, panggil list_state untuk projek yang sama, baca ringkasan dan laporkan lag. Kekalkan rujukan sumber. Laporkan nama projek dan UUID Event yang benar-benar dikembalikan. Sejarah kosong ialah hasil sah; jangan reka Event.

Jika akses ditolak atau projek tiada, semak akaun OAuth dan keahlian. Jangan tukar atau cipta projek sebagai jalan pintas. Selesaikan hanya selepas bacaan sebenar berjaya; jangan tulis Event ujian tanpa permintaan jelas.
