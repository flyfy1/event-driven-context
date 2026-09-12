# 生产部署与 Raspberry Pi 迁移

[English](production-deployment.md) | 简体中文

## 目标拓扑

- 后端主机：`songyy-pi`，使用 NVMe 的 Ubuntu 24.04 ARM64。
- 服务：systemd `event-context.service`，以 `songyy:service-admins` 运行，只监听 `127.0.0.1:8401`。
- 公网 API、OAuth 与 MCP：Cloudflare Tunnel `integ-pi` 把 `context-api.integ.life` 直接路由至 `http://localhost:8401`。
- 前端：GitHub Pages 继续提供 `context.integ.life`；公网 API 地址不变。
- 持久化：`/var/lib/event-driven-context/context.db` 保存身份与授权；`/var/lib/event-driven-context/data/` 保存 append-only 项目 Event、State、原始文件、Notes revision 与插件状态。
- Release：`/opt/event-driven-context/releases/<timestamp>-<commit>`，`/opt/event-driven-context/current` 指向当前版本。
- 密钥：只允许 root 读取的 `/etc/event-context.env`。不得把其值写入 Git、日志、工作记录或对话。

核心采用文件系统单写入者契约。切流后绝不能让 Pi 与 GCE 同时承担写入。

## 日常发布

从干净且已推送的 commit 运行：

```sh
make deploy-prod
```

部署脚本执行 `make check`，构建带 commit 信息的 Linux ARM64 静态 `edc-server` 与 `edc`，通过 SSH target `pi` 上传；目标已有数据时，会在停服后用 SQLite backup API 备份身份库并归档完整数据，然后安装不可变 release 并验证 loopback 健康。如需使用同一台 Pi 的另一个 SSH alias，只设置 `EDC_DEPLOY_SSH_TARGET`。

每次发布都要独立验证：

1. `event-context.service` active/enabled，进程对应预期 release，且只有 `127.0.0.1:8401` 在监听。
2. `https://context-api.integ.life/healthz` 返回预期版本与 request ID，该 request ID 可在 Pi journal 中找到。
3. OAuth discovery 与未认证 MCP challenge 仍指向 `https://context-api.integ.life`。
4. 公网 Web 真实登录后可恢复项目列表、既有 Event 与来源身份、项目共享及 State 来源链接。
5. 一次受控追加或幂等重放只出现在 Pi 存储，已停止的 GCE 副本中不存在。只有产品契约允许时才清理临时记录或 token；append-only Event 必须保留。

## 首次切流

1. 在不读取内容和密钥值的前提下记录源 release、服务身份、SQLite 完整性、V2 计数、数据大小与 hash。
2. GCE 仍是公网写入者时，先在 Pi 安装 ARM64 release 与预复制数据；验证 Pi loopback 行为和精确计数。
3. 停止 GCE `event-context.service`，进入短暂维护窗口；同一事务中停止自动 Notes worker。
4. 写入者停止后用 SQLite backup API 生成最终数据库快照并归档完整 `data/`。计算 hash，通过 SSH stream 传到 Pi 的 root-only 暂存目录。
5. 以 `0600` 安装 Pi `/etc/event-context.env`，用正确运行身份恢复最终数据库与数据，启动 Pi `event-context.service`，比较完整性、hash、计数和 loopback API。
6. 把 Cloudflare Tunnel 中 `context-api.integ.life` 的 Published application 改为 `http://localhost:8401`，域名与公网 URL 不变。
7. 验证公网健康、路由身份、OAuth、MCP 与真实浏览器流程，并在 Pi journal 中确认新的公网 request。
8. 禁用 GCE 应用与代理 unit，确认 GCE 的 8401 与 direct proxy listener 已关闭；保留 unit、环境、release、原始数据和最终快照作为回滚材料。

## 备份与回滚

应用数据库和 `data/` 是同一个一致性边界。可用备份必须同时包含 SQLite backup API 输出与完整的停写数据树。每次传输后都验证 SQLite integrity 与 SHA-256。

Pi 接受生产写入后，不能直接启动使用旧数据的 GCE。必须先冻结 Pi 写入，创建新的 Pi 一致性备份，把最新备份恢复到 GCE，本地验证后再切 Tunnel 或 DNS，并确保只有选定的一方写入。旧发布命令刻意带保护：

```sh
make deploy-legacy-gce
```

该目标会设置所需的 `ALLOW_LEGACY_GCE_DEPLOY=1`。它提供回滚能力，不代表允许同时运行两个生产写入者。
