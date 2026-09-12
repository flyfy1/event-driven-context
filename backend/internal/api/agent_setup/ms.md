# Sambungkan agent pengekodan setempat ke Event-driven Context

## Projek sasaran

`{{.Project}}`

{{if .HasProject}}ID projek dinyatakan di atas. Gunakannya dalam setiap arahan projek walaupun dokumen dimuat turun tanpa URL asal. Panduan ini tidak membuktikan projek wujud atau pengguna mempunyai akses; sahkan akses melalui CLI.{{else}}Belum ada projek dipilih. Tanya pengguna projek sedia ada sebelum mengkonfigurasi atau membaca konteks. PROJECT_ID di bawah hanyalah ruang letak. Jangan cipta projek secara automatik.{{end}}

## Matlamat

Sambungkan Codex atau Claude Code setempat melalui CLI `edc` yang telah log masuk, pasang Skill pencatat rasmi dan sahkan projek tepat dengan bacaan CLI sebenar. Jangan konfigurasi MCP untuk agent pengekodan setempat.

- Pelayan API: `{{.APIURL}}`
- Skill pencatat: `{{.SkillURL}}`

CLI membaca konfigurasi log masuk peribadinya sendiri. Jangan buka fail itu, salin atau cetak tokennya, atau minta pengguna menampal token dalam perbualan. Semak fail projek sedia ada dan kekalkan kandungan lain.

## 1. Cari dan sahkan CLI

Gunakan `edc` sedia ada dalam `PATH`, atau `./bin/edc` dari repositori. Jangan muat turun dan jalankan binari yang tidak dikenali.

```sh
edc help
edc --server {{.APIURL}} whoami
```

Jika `whoami` belum disahkan, berhenti dan minta pengguna menjalankan `edc --server {{.APIURL}} login --username USERNAME` secara peribadi dalam terminal. Jika konfigurasi bukan lalai digunakan, ulang laluan `--config /absolute/path/to/config.json` yang sama pada setiap arahan. Jangan baca fail itu untuk mendapatkan token.

## 2. Ikat direktori dan sahkan projek

Jalankan dalam direktori kerja:

```sh
edc --server {{.APIURL}} project list
edc --server {{.APIURL}} link {{.Project}}
edc --server {{.APIURL}} status
```

Jika projek tiada atau akses ditolak, semak akaun CLI dan keahlian. Jangan tukar atau cipta projek sebagai jalan pintas.

## 3. Pasang Skill pencatat

Baca Skill tepat daripada URL di atas. Bandingkan sasaran sedia ada sebelum menggantikannya dan kekalkan suntingan pengguna.

- Codex: simpan sebagai `.agents/skills/edc-recorder/SKILL.md`.
- Claude Code: pratonton `edc --server {{.APIURL}} setup claude-code`; selepas menyemak perubahan, jalankan `edc --server {{.APIURL}} setup --apply claude-code`. Ia memasang Skill dan hook rakaman pilihan, bukan MCP. Hook projek berkongsi memerlukan pengesahan berasingan.

Jangan commit kelayakan CLI, konfigurasi peribadi atau keadaan klien setempat.

## 4. Buktikan sambungan CLI langsung

Baca projek melalui `edc`, bukan MCP:

```sh
edc --server {{.APIURL}} query --project {{.Project}} --limit 5
edc --server {{.APIURL}} state list --project {{.Project}}
```

Laporkan nama projek sebenar, UUID Event yang dikembalikan dan sebarang lag State atau had liputan. Sejarah kosong ialah hasil yang sah. Persediaan selesai hanya selepas bacaan CLI sebenar berjaya; jangan tulis Event ujian tanpa permintaan jelas.
