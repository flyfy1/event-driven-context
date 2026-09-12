# अपने स्थानीय coding agent को Event-driven Context से जोड़ें

## लक्ष्य प्रोजेक्ट

`{{.Project}}`

{{if .HasProject}}प्रोजेक्ट ID ऊपर स्पष्ट है। दस्तावेज़ मूल URL के बिना डाउनलोड होने पर भी हर project command में यही ID उपयोग करें। यह गाइड प्रोजेक्ट के अस्तित्व या उपयोगकर्ता की पहुँच का प्रमाण नहीं है; CLI से वास्तविक जाँच करें।{{else}}कोई प्रोजेक्ट चुना नहीं गया है। Configure या context पढ़ने से पहले उपयोगकर्ता से मौजूदा प्रोजेक्ट पूछें। नीचे PROJECT_ID केवल placeholder है। अपने आप प्रोजेक्ट न बनाएँ।{{end}}

## उद्देश्य

स्थानीय Codex या Claude Code को login किए हुए `edc` CLI के जरिए जोड़ें, आधिकारिक recorder Skill install करें और उसी प्रोजेक्ट को CLI से वास्तव में पढ़कर जाँचें। स्थानीय coding agent के लिए MCP configure न करें।

- API server: `{{.APIURL}}`
- Recorder Skill: `{{.SkillURL}}`

CLI अपना निजी login config स्वयं पढ़ता है। उस file को न खोलें, उसका token copy या print न करें और उपयोगकर्ता से token chat में paste करने को न कहें। मौजूदा project files पहले जाँचें और दूसरी सामग्री बचाएँ।

## 1. CLI खोजें और login जाँचें

`PATH` में मौजूद `edc` या repository का `./bin/edc` उपयोग करें। कोई अज्ञात binary download करके न चलाएँ।

```sh
edc help
edc --server {{.APIURL}} whoami
```

यदि `whoami` authenticated नहीं है, रुकें और उपयोगकर्ता से अपने terminal में निजी तौर पर `edc --server {{.APIURL}} login --username USERNAME` चलाने को कहें। Non-default config होने पर उपयोगकर्ता का वही `--config /absolute/path/to/config.json` हर command में दोहराएँ। Token निकालने के लिए config file न पढ़ें।

## 2. Directory बाँधें और project जाँचें

Working directory में चलाएँ:

```sh
edc --server {{.APIURL}} project list
edc --server {{.APIURL}} link {{.Project}}
edc --server {{.APIURL}} status
```

Project न मिले या access deny हो तो CLI account और membership जाँचें। Workaround के रूप में दूसरा project न चुनें या बनाएँ।

## 3. Recorder Skill install करें

ऊपर दिए URL से exact Skill पहले पढ़ें। मौजूदा target बदलने से पहले तुलना करें और user edits बचाएँ।

- Codex: `.agents/skills/edc-recorder/SKILL.md` में save करें।
- Claude Code: पहले `edc --server {{.APIURL}} setup claude-code` का preview देखें; बदलाव जाँचने के बाद `edc --server {{.APIURL}} setup --apply claude-code` चलाएँ। यह Skill और वैकल्पिक capture hooks install करता है, MCP नहीं। Shared-project hooks के लिए अलग स्पष्ट पुष्टि चाहिए।

CLI credentials, private config या client-local state commit न करें।

## 4. Direct CLI connection सिद्ध करें

Project को `edc` से पढ़ें, MCP से नहीं:

```sh
edc --server {{.APIURL}} query --project {{.Project}} --limit 5
edc --server {{.APIURL}} state list --project {{.Project}}
```

वास्तविक project name, लौटे Event UUID और State lag या coverage limits बताएँ। खाली history भी वैध है; Event न गढ़ें। वास्तविक CLI read सफल होने पर ही setup पूरा है। स्पष्ट अनुरोध के बिना test Event न लिखें।

## वैकल्पिक: Memory Recall इंस्टॉल करें

जानकारी खोजने के लिए `{{.RecallSkillURL}}` पढ़ें और मौजूदा बदलाव सुरक्षित रखते हुए इसे `.agents/skills/memory-recall/SKILL.md` (Codex) या `.claude/skills/memory-recall/SKILL.md` (Claude Code) में सेव करें। `$memory-recall` के साथ प्रोजेक्ट `{{.Project}}` और अपना प्रश्न दें। यह प्रकाशित नोट स्थानीय फ़ोल्डर में सिंक करता है, संबंधित फ़ाइलें पढ़ता है और ज़रूरत पर CLI से स्रोत Events प्राप्त करता है। Recall रिकॉर्डिंग hook चालू नहीं करता और दूरस्थ रिकॉर्ड नहीं लिखता।
