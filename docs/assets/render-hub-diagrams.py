#!/usr/bin/env python3
"""Render the bilingual documentation SVGs using only Python's standard library.

Run: python3 docs/assets/render-hub-diagrams.py
The source contains no account credentials or deployment-specific state.
"""

from html import escape
from pathlib import Path

ROOT = Path(__file__).resolve().parent
INK = "#142b43"
MUTED = "#4e6378"
BLUE = "#2563a6"
GREEN = "#167862"


class SVG:
    def __init__(self, width, height, title, description):
        self.parts = [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
            f'viewBox="0 0 {width} {height}" role="img" aria-labelledby="title description">',
            f"<title id=\"title\">{escape(title)}</title>",
            f"<desc id=\"description\">{escape(description)}</desc>",
            '<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" '
            'markerWidth="7" markerHeight="7" orient="auto-start-reverse">'
            '<path d="M0 0L10 5L0 10Z" fill="#4e6378"/></marker></defs>',
            '<style>text {font-family: "Noto Sans CJK SC", "Noto Sans", sans-serif; '
            'fill: #142b43} .line {fill:none;stroke:#4e6378;stroke-width:2.2}</style>',
            f'<rect width="{width}" height="{height}" fill="#ffffff"/>',
        ]

    def rect(self, x, y, w, h, fill="#fff", stroke="#c6d4e2", radius=14, dash=False):
        dashed = ' stroke-dasharray="7 5"' if dash else ""
        self.parts.append(
            f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{radius}" '
            f'fill="{fill}" stroke="{stroke}" stroke-width="1.5"{dashed}/>'
        )

    def text(self, x, y, value, size=20, weight=400, color=INK, anchor="start"):
        self.parts.append(
            f'<text x="{x}" y="{y}" font-size="{size}" font-weight="{weight}" '
            f'style="fill:{color}" text-anchor="{anchor}">{escape(value)}</text>'
        )

    def box(self, x, y, w, h, title, lines, fill="#fff", stroke="#c6d4e2"):
        self.rect(x, y, w, h, fill, stroke)
        self.text(x + 20, y + 34, title, 22, 700)
        for i, line in enumerate(lines):
            self.text(x + 20, y + 65 + i * 28, line, 18, color=MUTED)

    def line(self, points, arrow=True, dashed=False):
        path = "M" + " L".join(f"{x} {y}" for x, y in points)
        marker = ' marker-end="url(#arrow)"' if arrow else ""
        dash = ' stroke-dasharray="6 5"' if dashed else ""
        self.parts.append(f'<path d="{path}" class="line"{marker}{dash}/>')

    def save(self, filename):
        (ROOT / filename).write_text("\n".join(self.parts + ["</svg>"]) + "\n")


def overview(zh):
    def t(en, cn):
        return cn if zh else en

    s = SVG(1600, 1280, t("Data hub architecture", "数据 Hub 架构"), t(
        "Owner setup and Agent operation grants are independent. Project MCP is a separate access path.",
        "用户接入与 Agent 操作授权独立管理；项目 MCP 使用独立权限。"))
    s.text(60, 55, t("Event-driven Context", "Event-driven Context"), 32, 700)
    s.text(60, 90, t("Data hub architecture and account authorization · v0.1.2",
                      "数据 Hub 架构与多账号授权 · v0.1.2"), 23, color=MUTED)
    s.box(60, 130, 410, 105, t("Owner", "用户"), [
        t("Browser / Android / owner CLI", "网页 / Android / 用户 CLI"),
        t("Sign in · connect accounts · approve", "登录 · 接入账号 · 审批")], "#edf7f4", "#9bcebf")
    s.box(550, 130, 490, 105, t("Agent and scripts", "Agent 与脚本"), [
        t("edc + separate private Agent credential", "edc + 独立私密 Agent 凭证"),
        t("Discover · request · read", "发现连接 · 申请操作 · 读取")], "#edf4fc", "#a1bedf")
    s.box(1140, 130, 400, 105, "ChatGPT / Claude Web", [
        t("Context project tools", "Context 项目工具"),
        t("Project MCP OAuth credential", "项目 MCP OAuth 凭证")], "#f2effa", "#beb2db")
    s.rect(40, 295, 1520, 640, "#f8fafc", "#b9c8d7")
    s.text(330, 326, t("Deployer-controlled server and storage",
                      "部署者控制的服务端与文件系统"), 21, 700)
    s.rect(70, 345, 970, 550, "#fff", "#d2dce7")
    s.rect(1130, 345, 400, 550, "#fff", "#beb2db", dash=True)
    s.box(100, 370, 370, 120, t("Owner setup and approval API", "用户接入与审批 API"), [
        t("Owner session; Google OAuth + PKCE", "用户会话；Google OAuth + PKCE"),
        t("Source credentials and grant decisions", "数据源凭据与授权审批")], "#edf7f4", "#9bcebf")
    s.box(550, 370, 460, 120, t("Agent read authorization gate", "Agent 读取权限校验"), [
        t("Agent + owner + connection + operation", "Agent + 用户 + 连接 + 操作"),
        t("Expiry + constraints; checked twice", "有效期 + 限制条件；前后各检查一次")], "#edf4fc", "#a1bedf")
    s.box(100, 550, 910, 120, t("Source adapters and ingestion", "数据源适配与接收"), [
        t("Fixed API endpoints · owner-selected import parser · signed webhook receiver",
          "固定 API 端点 · 用户选定文件的导入解析器 · 签名 webhook 接收器"),
        t("Server-held credentials; bounded results; no automatic project Event writes",
          "凭据留在服务端；结果有大小限制；不自动写入项目 Events")], "#f3f7fb")
    s.box(100, 755, 430, 120, "SQLite", [
        t("Identity, connections and grants", "身份、连接与授权"),
        t("Encrypted secrets and Business receipts", "加密凭据与 Business 原始回执")], "#f3f7fb")
    s.box(580, 755, 430, 120, "<data>/hub-imports", [
        t("Immutable originals and manifests", "不可变原件与清单"),
        t("Plaintext files; private permissions", "明文文件；私密文件权限")], "#fff8ec", "#dfc392")
    s.box(1160, 370, 340, 120, t("Project /mcp", "项目 /mcp"), [
        t("Context OAuth scopes", "Context OAuth scope"),
        t("+ project membership", "+ 项目成员权限")], "#f2effa", "#beb2db")
    s.box(1160, 605, 340, 150, t("Project storage", "项目存储"), [
        "Events / Files / metadata",
        t("Append-only via product interfaces", "通过产品接口只能追加"),
        t("Derived State and processing", "派生 State 与处理")], "#f2effa", "#beb2db")
    s.text(1155, 814, t("Project and Hub grants are separate.", "项目与 Hub 授权独立。"), 18, 700)
    s.text(1155, 844, t("No automatic source-to-project archive.", "不会自动归档数据源到项目。"), 18, color=MUTED)
    for points in [
        [(265, 235), (265, 370)], [(795, 235), (795, 370)],
        [(1340, 235), (1340, 370)], [(285, 490), (285, 550)],
        [(780, 490), (780, 550)], [(315, 670), (315, 755)],
        [(795, 670), (795, 755)], [(1330, 490), (1330, 605)],
    ]:
        s.line(points)
    s.text(338, 713, t("Private server storage", "服务端私密存储"), 18, color=MUTED)
    s.text(60, 982, t("SOURCE DATA: ON-DEMAND API READS / OWNER UPLOADS / WEBHOOK PUSH",
                      "数据输入：按需 API 读取 / 用户主动上传 / webhook 推送"), 21, 700)
    s.line([(225, 1040), (225, 1010), (1365, 1010)], arrow=False)
    s.line([(1070, 1010), (1070, 610), (1010, 610)])
    for x in (605, 985, 1365):
        s.line([(x, 1040), (x, 1010)], arrow=False)
    s.box(60, 1040, 330, 130, t("Google accounts", "Google 账号"), [
        "Gmail / GCal / Drive / ...",
        t("Browser OAuth per service/account", "按服务、账号完成浏览器 OAuth"),
        t("Managed token refresh", "托管 token 刷新")], "#edf7f4", "#9bcebf")
    s.box(440, 1040, 330, 130, t("Manual source credentials", "手动提供的数据源凭据"), [
        "Graph / SaaS / Bot / DAV",
        t("Owner token or app password", "用户 token 或 App 专用密码"),
        t("Operator-controlled renewal", "运维负责续期")], "#edf4fc", "#a1bedf")
    s.box(820, 1040, 330, 130, t("Personal chat exports", "个人聊天导出"), [
        t("WhatsApp / Telegram / WeChat", "WhatsApp / Telegram / 微信"),
        "TXT / ZIP / JSON / CSV",
        t("Selected snapshots, not live sync", "用户选定快照；不实时同步")], "#fff8ec", "#dfc392")
    s.box(1200, 1040, 330, 130, "WhatsApp Business", [
        t("Meta signature + WABA/phone", "Meta 签名 + WABA / 号码绑定"),
        t("New webhook deliveries only", "仅新收到的 webhook 推送"),
        t("Encrypted retained originals", "加密保留原始推送")], "#f2effa", "#beb2db")
    s.text(60, 1218, t("One connection = owner + provider + account. One grant = Agent + connection + operation + expiry + supported constraints.",
                       "一个连接 = 用户 + 数据源 + 账号。一次授权 = Agent + 连接 + 操作 + 有效期 + 已支持的限制条件。"), 20, 700)
    s.text(60, 1254, t("Self-hosting controls persistence. Content forwarded to external models may leave this deployment.",
                       "自托管控制持久化位置；转交给外部模型的内容仍可能离开此部署。"), 18, color=MUTED)
    s.save("hub-architecture" + (".cn" if zh else "") + ".svg")


def sequence(filename, title, subtitle, participants, messages, footer):
    width = 1500
    height = 240 + len(messages) * 77 + 65
    s = SVG(width, height, title, subtitle)
    s.text(50, 50, title, 30, 700)
    s.text(50, 88, subtitle, 21, color=MUTED)
    xs = [130 + i * 1240 / (len(participants) - 1) for i in range(len(participants))]
    for x, label in zip(xs, participants):
        s.rect(x - 108, 125, 216, 65, "#edf4fc", "#a1bedf")
        s.text(x, 166, label, 21, 700, anchor="middle")
        s.line([(x, 190), (x, height - 95)], arrow=False, dashed=True)
    for i, (a, b, label, dashed) in enumerate(messages):
        y = 247 + i * 77
        xa, xb = xs[a], xs[b]
        if a == b:
            # Put self-check labels to the left of the participant's small loop.
            s.text(xa - 16, y - 12, f"{i + 1}. {label}", 19, anchor="end")
            s.line([(xa, y), (xa + 48, y), (xa + 48, y + 22), (xa, y + 22)])
        else:
            s.text((xa + xb) / 2, y - 12, f"{i + 1}. {label}", 19, anchor="middle")
            s.line([(xa, y), (xb, y)], dashed=dashed)
    s.text(50, height - 32, footer, 20, 700, color=GREEN)
    s.save(filename)


def sequences(zh):
    def t(en, cn):
        return cn if zh else en

    suffix = ".cn" if zh else ""
    sequence("hub-google-authorization" + suffix + ".svg",
             t("Google account authorization", "Google 账号授权时序"),
             t("Repeat for every selected service and account; Agent grants remain separate.",
               "为每个选定的服务、账号重复流程；Agent 操作授权仍需单独批准。"),
             [t("Owner browser", "用户浏览器"), "Context Hub", "Google", t("Private storage", "私密存储")], [
                 (0, 1, t("Start with owner session + provider", "用户会话启动，指定数据源"), False),
                 (1, 3, t("Bind state + PKCE to owner/provider; 10 minutes, one use", "state + PKCE 绑定用户和数据源；10 分钟单次有效"), False),
                 (1, 0, t("Authorization URL + callback cookie", "授权 URL + 回调 cookie"), True),
                 (0, 2, t("Select account; consent to service scopes", "选择账号，批准该服务的 scope"), False),
                 (2, 0, t("Redirect with code + state", "携带 code + state 重定向"), True),
                 (0, 1, t("Callback + matching browser cookie", "回调 + 匹配的浏览器 cookie"), False),
                 (1, 2, t("Exchange code with PKCE; check scopes + account", "PKCE 换 token，验证 scope 与账号"), False),
                 (1, 3, t("Save encrypted connection; reconnect revokes old grants", "加密保存连接；重新连接撤销旧授权"), False),
                 (1, 0, t("Account connected; no Agent read grant yet", "账号连接完成；尚未授予 Agent 读取权限"), True),
             ], t("Application credentials identify the app. Only each account's consent grants source data access.",
                  "应用凭据只标识应用；各账号的同意才授予数据源访问权。"))
    sequence("hub-agent-authorization" + suffix + ".svg",
             t("Agent pairing and operation authorization", "Agent 配对与操作授权时序"),
             t("Pairing permits discovery; each read operation needs a separate owner grant.",
               "配对只允许发现连接；每个读取操作都需要用户单独授权。"),
             [t("Agent CLI", "Agent CLI"), t("Owner", "用户"), "Context Hub", t("Source / snapshot", "数据源 / 快照")], [
                 (0, 2, t("Register pairing request", "登记配对请求"), False),
                 (2, 0, t("Private credential + verification code; pending 15 minutes", "私密凭证 + 验证码；待配对有效期 15 分钟"), True),
                 (1, 2, t("Compare code; approve pairing", "核对验证码，批准配对"), False),
                 (0, 2, t("Discover configured connections", "发现已配置连接"), False),
                 (0, 2, t("Request connection + operation + duration + constraints", "申请连接 + 操作 + 时长 + 限制条件"), False),
                 (1, 2, t("Approve this operation request", "批准这项操作请求"), False),
                 (0, 2, t("source read with exact connection + operation", "source read 指定连接和操作"), False),
                 (2, 2, t("Check identity, grant and bounds", "检查身份、授权与范围"), False),
                 (2, 3, t("Bounded read; server-held credential", "有界读取；凭据留在服务端"), False),
                 (3, 2, t("Return source result", "返回数据源结果"), True),
                 (2, 2, t("Recheck access before delivery", "返回前再次检查权限"), False),
                 (2, 0, t("Return data without provider credentials", "返回数据，不返回第三方凭据"), True),
             ], t("Mailbox A approval does not cover mailbox B. Calendar content requires calendar/time constraints.",
                  "邮箱 A 的授权不覆盖邮箱 B；日历内容读取必须限定日历与时间窗口。"))


if __name__ == "__main__":
    for chinese in (False, True):
        overview(chinese)
        sequences(chinese)
