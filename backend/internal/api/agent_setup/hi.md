# अपने coding agent को Event-driven Context से जोड़ें

## लक्ष्य प्रोजेक्ट

`{{.Project}}`

{{if .HasProject}}प्रोजेक्ट ID ऊपर स्पष्ट रूप से दिया गया है। दस्तावेज़ मूल URL के बिना डाउनलोड होने पर भी हर प्रोजेक्ट कॉल में यही ID उपयोग करें। यह गाइड प्रोजेक्ट के अस्तित्व या उपयोगकर्ता की पहुँच का प्रमाण नहीं है; OAuth के बाद सदस्यता जाँचें।{{else}}कोई प्रोजेक्ट चुना नहीं गया है। सेटअप या संदर्भ पढ़ने से पहले उपयोगकर्ता से मौजूदा प्रोजेक्ट पूछें। नीचे PROJECT_ID केवल प्लेसहोल्डर है। अपने आप प्रोजेक्ट न बनाएँ।{{end}}

## उद्देश्य

इस कार्य डायरेक्टरी को remote MCP से उपयोगकर्ता के मौजूदा खाते से जोड़ें, आधिकारिक recorder Skill स्थापित करें और उसी प्रोजेक्ट को वास्तव में पढ़कर सत्यापित करें। केवल कॉन्फ़िगरेशन फ़ाइलें प्रमाण नहीं हैं।

- MCP endpoint: `{{.MCPURL}}`
- Recorder Skill: `{{.SkillURL}}`

उपयोगकर्ता से बातचीत में access token पेस्ट करने को न कहें। उनके ब्राउज़र में OAuth करें। मौजूदा सेटिंग जाँचें, अन्य entries बनाए रखें और अलग अनुरोध के बिना automatic capture hook चालू न करें।

## 1. Remote MCP कॉन्फ़िगर करें

वर्तमान कार्य चला रहे क्लाइंट के चरण चुनें। event-context entry पहले से हो तो endpoint जाँचें और अन्य सेटिंग बनाए रखें; दूसरी सेवा वाली entry चुपचाप न बदलें।

### Codex

पहले जाँचें। Entry न होने पर ही सर्वर जोड़ें:

```sh
codex mcp get event-context
codex mcp add event-context \
  --url {{.MCPURL}} \
  --oauth-resource {{.MCPURL}}
```

दोनों scopes के साथ OAuth पूरा करें। ज़रूरत पर उपयोगकर्ता के लिए authorization पेज खोलें और कमांड से सफलता की पुष्टि की प्रतीक्षा करें:

```sh
codex mcp login event-context --scopes context:read,context:write
```

### Claude Code

पहले जाँचें। Entry न होने पर ही सर्वर जोड़ें:

```sh
claude mcp get event-context
claude mcp add --transport http --scope local \
  event-context {{.MCPURL}}
```

Claude Code में /mcp खोलें, event-context चुनें और ब्राउज़र में OAuth पूरा करें। Protected-resource discovery और dynamic registration उपयोग करें; static token या client secret न डालें।

## 2. Recorder Skill स्थापित करें

ऊपर दिए URL से मूल Skill पढ़ें और डाउनलोड करें। वर्तमान प्रोजेक्ट में क्लाइंट के अनुसार इस पथ पर सहेजें:

- Codex: `.agents/skills/edc-recorder/SKILL.md`
- Claude Code: `.claude/skills/edc-recorder/SKILL.md`

मौजूदा फ़ाइल बदलने से पहले तुलना करें और उपयोगकर्ता के बदलाव बनाए रखें। OAuth credentials या निजी client state commit न करें।

## 3. वास्तविक कनेक्शन सत्यापित करें

MCP फिर जोड़ें या नया agent सत्र खोलें ताकि सर्वर और Skill लोड हों। list_projects से लक्ष्य प्रोजेक्ट ID की मौजूदगी जाँचें। फिर इन मापदंडों से query_events कॉल करें:

```json
{
  "project_id": "{{.Project}}",
  "limit": 5
}
```

उपलब्ध हो तो उसी प्रोजेक्ट के लिए list_state कॉल करें, सार पढ़ें और lag बताएँ। स्रोत संदर्भ बनाए रखें। वास्तविक प्रोजेक्ट नाम और लौटे Event UUID बताएँ। खाली इतिहास वैध परिणाम है; Event न गढ़ें।

पहुँच न मिले या प्रोजेक्ट न मिले तो OAuth खाता और सदस्यता जाँचें। रास्ता निकालने के लिए प्रोजेक्ट न बदलें या बनाएँ। वास्तविक पढ़ने की सफलता के बाद ही पूरा मानें; स्पष्ट अनुरोध के बिना परीक्षण Event न लिखें।
