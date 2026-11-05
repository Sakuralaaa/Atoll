package templates

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// RandomPlaceholder in an app template env value or config file becomes a fresh random secret.
const RandomPlaceholder = "{{RANDOM}}"

// RandomBase64Placeholder becomes 32 fresh random bytes in standard base64 (for apps that
// want a base64 AES-256 key).
const RandomBase64Placeholder = "{{RANDOM_BASE64}}"

func web(port int) []api.Port {
	return []api.Port{{Name: "web", ContainerPort: port, Protocol: "http"}}
}

func vol(name, path string) api.Volume { return api.Volume{Name: name, MountPath: path} }

// InputRef is the placeholder for the template input key ({{INPUT:KEY}}).
func InputRef(key string) string { return inputPrefix + key + "}}" }

const inputPrefix = "{{INPUT:"

func secretInput(key, label, labelEn string) api.AppTemplateInput {
	return api.AppTemplateInput{Key: key, Label: label, LabelEn: labelEn, Secret: true}
}

func cfg(path, content string) []api.ConfigFile {
	return []api.ConfigFile{{Path: path, Content: content}}
}

// cpaConfig is CLIProxyAPI's config.yaml in its v8 layout (see config.example.yaml upstream).
// It is mounted read-only as a seed (/CLIProxyAPI/config.seed.yaml) and copied once to the
// "config" volume, where CPA (and its management panel) edit the live copy. The management key
// comes from MANAGEMENT_PASSWORD.
const cpaConfig = `# CLIProxyAPI config (Atoll one-click app). Full reference: config.example.yaml in
# github.com/router-for-me/CLIProxyAPI. The live copy is /CLIProxyAPI/conf/config.yaml (on the
# "config" volume): edit it in the CPA panel or on Atoll's Files page; it survives redeploys.
# This seed is only copied when that file does not exist yet.
config-version: 8

server:
  port: 8317

management:
  # The management key is the MANAGEMENT_PASSWORD env var of this service (set at creation).
  allow-remote: true
  secret-key: ""

access:
  # Keys your clients send as "Authorization: Bearer <key>".
  api-keys:
    - "sk-{{RANDOM}}"

oauth:
  auth-dir: "/root/.cli-proxy-api"

observability:
  usage:
    # Needed for request monitoring in CPA Manager Plus.
    usage-statistics-enabled: true
    redis-usage-queue-retention-seconds: 60
`

// grok2apiConfig is grok2api's config.yaml; everything not listed keeps the built-in default
// (SQLite at /app/data/backend.db, media in /app/data/media, in-memory runtime store).
const grok2apiConfig = `# grok2api config (Atoll one-click app). Full reference: config.example.yaml in
# github.com/chenyme/grok2api. Edit it in the service's configs, then redeploy.
server:
  listen: "0.0.0.0:8000"

auth:
  # Set to true once the panel is only opened over HTTPS (a bound domain).
  secureCookies: false

secrets:
  jwtSecret: "{{RANDOM}}"
  # Keep this forever once accounts are added: changing it makes stored credentials unreadable.
  credentialEncryptionKey: "{{RANDOM_BASE64}}"

bootstrapAdmin:
  # Used only while the database has no admin. Change the password after signing in.
  username: "admin"
  password: "{{INPUT:ADMIN_PASSWORD}}"
`

func env(kv ...string) []api.EnvVar {
	out := make([]api.EnvVar, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		v := kv[i+1]
		out = append(out, api.EnvVar{Key: kv[i], Value: v, Secret: strings.Contains(v, RandomPlaceholder) || strings.Contains(v, inputPrefix) || strings.Contains(v, "PASSWORD}") || strings.Contains(v, "CONNECTION_STRING}")})
	}
	return out
}

// apps are the one-click apps, in display order. Images are official ones; tags are
// either a major version or the project's own rolling tag, so image update checks apply.
var apps = []api.AppTemplate{
	{
		ID: "halo", Name: "Halo", Category: "blog", Color: "#0f5bf7", Website: "https://www.halo.run",
		Description: "国内流行的开源建站工具，主题和插件丰富，带 PostgreSQL", DescriptionEn: "Popular open-source site builder with themes and plugins, with PostgreSQL",
		MinMemMB: 1024,
		Note:     "打开后按页面引导创建管理员账号。", NoteEn: "Open it and follow the setup page to create the admin account.",
		Services: []api.AppTemplateService{
			{Name: "postgres", Template: "postgres"},
			{Name: "halo", Image: "halohub/halo:2", Ports: web(8090), Volumes: []api.Volume{vol("data", "/root/.halo2")}, Main: true, MemLimitMB: 1024,
				Env: env(
					"SPRING_R2DBC_URL", "r2dbc:pool:postgresql://${POSTGRES_HOST}:${POSTGRES_PORT}/${POSTGRES_DATABASE}",
					"SPRING_R2DBC_USERNAME", "${POSTGRES_USERNAME}",
					"SPRING_R2DBC_PASSWORD", "${POSTGRES_PASSWORD}",
					"SPRING_SQL_INIT_PLATFORM", "postgresql",
				),
				DomainEnv: env("HALO_EXTERNAL_URL", "${ATOLL_WEB_URL}")},
		},
	},
	{
		ID: "ghost", Name: "Ghost", Category: "blog", Color: "#15171a", Website: "https://ghost.org",
		Description: "简洁好看的写作和会员订阅平台，带 MySQL", DescriptionEn: "Clean publishing and newsletter platform, with MySQL",
		NeedsDomain: true, MinMemMB: 768,
		Note: "后台地址是 域名/ghost，第一次打开时创建管理员。", NoteEn: "The admin is at your-domain/ghost; create the owner account on first visit.",
		Services: []api.AppTemplateService{
			{Name: "mysql", Template: "mysql"},
			{Name: "ghost", Image: "ghost:5-alpine", Ports: web(2368), Volumes: []api.Volume{vol("content", "/var/lib/ghost/content")}, Main: true,
				Env: env(
					"database__client", "mysql",
					"database__connection__host", "${MYSQL_HOST}",
					"database__connection__port", "${MYSQL_PORT}",
					"database__connection__user", "${MYSQL_USERNAME}",
					"database__connection__password", "${MYSQL_PASSWORD}",
					"database__connection__database", "${MYSQL_DATABASE}",
				),
				DomainEnv: env("url", "${ATOLL_WEB_URL}")},
		},
	},
	{
		ID: "papergrid", Name: "PaperGrid", Category: "blog", Color: "#8b7cf6", Website: "https://github.com/Sakuralaaa/blog",
		Description: "Atoll 风格的轻量博客，SQLite 单容器，内置 MCP 让 AI Agent 写文章", DescriptionEn: "Lightweight Atoll-styled blog on SQLite with a built-in MCP endpoint for AI agents",
		NeedsDomain: true, MinMemMB: 512,
		Note:   "管理员邮箱 admin@example.com，初始密码在环境变量 ADMIN_INIT_PASSWORD，登录后在后台修改。后台「接口密钥」页有 MCP 接入说明。",
		NoteEn: "Admin email is admin@example.com; the initial password is in the ADMIN_INIT_PASSWORD env var. Change it after signing in. The API Keys page explains MCP setup.",
		Services: []api.AppTemplateService{
			{Name: "papergrid", Image: "ghcr.io/sakuralaaa/blog:latest", Ports: web(3000), Volumes: []api.Volume{vol("data", "/data")}, Main: true, MemLimitMB: 512,
				Env: env(
					"DATABASE_URL", "file:/data/db.sqlite",
					"NEXTAUTH_SECRET", RandomPlaceholder,
					"ADMIN_INIT_PASSWORD", RandomPlaceholder,
					"AUTH_TRUST_HOST", "1",
					"NEXT_CACHE_DIR", "/data/.next-cache",
					"MEDIA_ROOT", "/data/uploads",
				),
				DomainEnv: env("NEXTAUTH_URL", "${ATOLL_WEB_URL}", "NEXT_PUBLIC_APP_URL", "${ATOLL_WEB_URL}")},
		},
	},
	{
		ID: "wordpress", Name: "WordPress", Category: "blog", Color: "#21759b", Website: "https://wordpress.org",
		Description: "全世界用得最多的博客和建站系统，带 MySQL", DescriptionEn: "The world's most used blogging and site system, with MySQL",
		MinMemMB: 768,
		Note:     "打开后按安装向导设置站点和管理员。", NoteEn: "Open it and run the install wizard to set up the site and admin.",
		Services: []api.AppTemplateService{
			{Name: "mysql", Template: "mysql"},
			{Name: "wordpress", Image: "wordpress:6-apache", Ports: web(80), Volumes: []api.Volume{vol("html", "/var/www/html")}, Main: true,
				Env: env(
					"WORDPRESS_DB_HOST", "${MYSQL_HOST}:${MYSQL_PORT}",
					"WORDPRESS_DB_USER", "${MYSQL_USERNAME}",
					"WORDPRESS_DB_PASSWORD", "${MYSQL_PASSWORD}",
					"WORDPRESS_DB_NAME", "${MYSQL_DATABASE}",
				)},
		},
	},
	{
		ID: "memos", Name: "Memos", Category: "tools", Color: "#f59e0b", Website: "https://usememos.com",
		Description: "轻量的碎碎念和笔记，像自己的私人微博", DescriptionEn: "Lightweight notes and memos, like a private microblog",
		MinMemMB: 128,
		Note:     "第一个注册的账号是管理员。", NoteEn: "The first account to sign up becomes the admin.",
		Services: []api.AppTemplateService{
			{Name: "memos", Image: "neosmemo/memos:stable", Ports: web(5230), Volumes: []api.Volume{vol("data", "/var/opt/memos")}, Main: true},
		},
	},
	{
		ID: "alist", Name: "AList", Category: "tools", Color: "#1890ff", Website: "https://alistgo.com",
		Description: "把各种网盘和存储挂成一个文件列表，支持在线预览", DescriptionEn: "Mounts cloud drives and storage into one file list with previews",
		MinMemMB: 128,
		Note:     "初始管理员密码在服务日志里（搜索 password）。", NoteEn: "The initial admin password is in the service logs (search for \"password\").",
		Services: []api.AppTemplateService{
			{Name: "alist", Image: "xhofe/alist:latest", Ports: web(5244), Volumes: []api.Volume{vol("data", "/opt/alist/data")}, Main: true},
		},
	},
	{
		ID: "vaultwarden", Name: "Vaultwarden", Category: "tools", Color: "#175ddc", Website: "https://github.com/dani-garcia/vaultwarden",
		Description: "兼容 Bitwarden 客户端的密码管理器服务端", DescriptionEn: "Password manager server compatible with Bitwarden clients",
		NeedsDomain: true, MinMemMB: 128,
		Note: "网页端必须通过 HTTPS 访问。注册完自己的账号后，建议把 SIGNUPS_ALLOWED 改成 false。", NoteEn: "The web vault needs HTTPS. After creating your account, set SIGNUPS_ALLOWED to false.",
		Services: []api.AppTemplateService{
			{Name: "vaultwarden", Image: "vaultwarden/server:latest", Ports: web(80), Volumes: []api.Volume{vol("data", "/data")}, Main: true,
				Env:       env("SIGNUPS_ALLOWED", "true"),
				DomainEnv: env("DOMAIN", "${ATOLL_WEB_URL}")},
		},
	},
	{
		ID: "freshrss", Name: "FreshRSS", Category: "tools", Color: "#0062be", Website: "https://freshrss.org",
		Description: "自托管 RSS 阅读器，支持 Fever / Google Reader API 客户端", DescriptionEn: "Self-hosted RSS reader with Fever / Google Reader API support",
		MinMemMB: 128,
		Services: []api.AppTemplateService{
			{Name: "freshrss", Image: "freshrss/freshrss:latest", Ports: web(80), Main: true,
				Volumes: []api.Volume{vol("data", "/var/www/FreshRSS/data"), vol("extensions", "/var/www/FreshRSS/extensions")},
				Env:     env("TZ", "Asia/Shanghai", "CRON_MIN", "*/20")},
		},
	},
	{
		ID: "it-tools", Name: "IT Tools", Category: "tools", Color: "#18a058", Website: "https://it-tools.tech",
		Description: "开发常用小工具合集：编码转换、哈希、UUID、JSON 格式化等", DescriptionEn: "Handy developer tools: encoders, hashes, UUIDs, JSON formatting and more",
		MinMemMB: 64,
		Services: []api.AppTemplateService{
			{Name: "it-tools", Image: "corentinth/it-tools:latest", Ports: web(80), Main: true},
		},
	},
	{
		ID: "excalidraw", Name: "Excalidraw", Category: "tools", Color: "#6965db", Website: "https://excalidraw.com",
		Description: "手绘风格的在线白板，画草图和流程图", DescriptionEn: "Hand-drawn style whiteboard for sketches and diagrams",
		MinMemMB: 64,
		Note:     "画布保存在浏览器本地。", NoteEn: "Drawings are stored in your browser.",
		Services: []api.AppTemplateService{
			{Name: "excalidraw", Image: "excalidraw/excalidraw:latest", Ports: web(80), Main: true},
		},
	},
	{
		ID: "flaresolverr", Name: "FlareSolverr", Category: "tools", Color: "#f38020", Website: "https://github.com/FlareSolverr/FlareSolverr",
		Description: "用无头 Chrome 过 Cloudflare 验证的代理接口，给 grok2api、Jackett 等程序调用", DescriptionEn: "Proxy API that passes Cloudflare challenges with headless Chrome, for grok2api, Jackett and similar tools",
		MinMemMB: 512,
		Note:     "只有 API，没有网页界面：程序调用 POST /v1。同一项目里的服务用 http://flaresolverr:8191 访问；其他项目要用就绑定域名。浏览器很吃内存，内存小的机器别开太多会话。",
		NoteEn:   "API only, no web UI: apps call POST /v1. Services in the same project reach it at http://flaresolverr:8191; bind a domain to use it from other projects. Browsers use a lot of memory, so keep sessions few on small servers.",
		Services: []api.AppTemplateService{
			{Name: "flaresolverr", Image: "ghcr.io/flaresolverr/flaresolverr:latest", Ports: web(8191), Main: true, MemLimitMB: 1024,
				Env: env("LOG_LEVEL", "info", "TZ", "Asia/Shanghai")},
		},
	},
	{
		ID: "outlook-email", Name: "OutlookEmail", Category: "tools", Color: "#0078d4", Website: "https://github.com/assast/outlookEmail",
		Description: "批量管理 Outlook / Hotmail 邮箱，网页里收信看验证码", DescriptionEn: "Manage many Outlook / Hotmail mailboxes and read mail and verification codes in the browser",
		MinMemMB: 256,
		Inputs:   []api.AppTemplateInput{secretInput("LOGIN_PASSWORD", "登录密码", "Login password")},
		Note:     "登录密码是创建时填写的那个；留空则自动生成，在环境变量 LOGIN_PASSWORD 里（只在第一次启动时生效，之后在网页设置里改）。SECRET_KEY 用来加密数据，不要改。镜像自带的「一键更新」已关闭，更新请在 Atoll 里重新部署。",
		NoteEn:   "The login password is the one you entered at creation; if left empty it is generated and kept in the LOGIN_PASSWORD env var (only used on first start; change it later in the web settings). SECRET_KEY encrypts the data, so don't change it. The image's built-in self-update is disabled; update by redeploying in Atoll.",
		Services: []api.AppTemplateService{
			{Name: "outlook-email", Image: "ghcr.io/assast/outlookemail:latest", Ports: web(5000), Volumes: []api.Volume{vol("data", "/app/data")}, Main: true,
				Env: env("LOGIN_PASSWORD", InputRef("LOGIN_PASSWORD"), "SECRET_KEY", RandomPlaceholder, "FLASK_ENV", "production", "DOCKER_UPDATE_ENABLED", "false")},
		},
	},
	{
		ID: "uptime-kuma", Name: "Uptime Kuma", Category: "ops", Color: "#5cdd8b", Website: "https://uptime.kuma.pet",
		Description: "好看的网站和服务可用性监控，支持多种通知和状态页", DescriptionEn: "Good-looking uptime monitoring with notifications and status pages",
		MinMemMB: 256,
		Note:     "第一次打开时创建管理员账号。", NoteEn: "Create the admin account on first visit.",
		Services: []api.AppTemplateService{
			{Name: "uptime-kuma", Image: "louislam/uptime-kuma:1", Ports: web(3001), Volumes: []api.Volume{vol("data", "/app/data")}, Main: true},
		},
	},
	{
		ID: "umami", Name: "Umami", Category: "ops", Color: "#000000", Website: "https://umami.is",
		Description: "注重隐私的网站访问统计，Google Analytics 的轻量替代，带 PostgreSQL", DescriptionEn: "Privacy-focused web analytics, a light Google Analytics alternative, with PostgreSQL",
		MinMemMB: 512,
		Note:     "默认账号 admin / umami，登录后立刻改密码。", NoteEn: "Default login is admin / umami; change the password right away.",
		Services: []api.AppTemplateService{
			{Name: "postgres", Template: "postgres"},
			{Name: "umami", Image: "ghcr.io/umami-software/umami:postgresql-latest", Ports: web(3000), Main: true,
				Env: env("DATABASE_URL", "${POSTGRES_CONNECTION_STRING}", "APP_SECRET", RandomPlaceholder)},
		},
	},
	{
		ID: "gitea", Name: "Gitea", Category: "dev", Color: "#609926", Website: "https://about.gitea.com",
		Description: "轻量的自托管 Git 服务，带 PostgreSQL", DescriptionEn: "Lightweight self-hosted Git service, with PostgreSQL",
		MinMemMB: 512,
		Note:     "第一次打开是安装页，数据库已经填好，直接创建管理员即可。只开放了 HTTP，Git 走 HTTPS 克隆。", NoteEn: "The first visit is the install page with the database prefilled; just create the admin. Only HTTP is exposed, so clone over HTTPS.",
		Services: []api.AppTemplateService{
			{Name: "postgres", Template: "postgres"},
			{Name: "gitea", Image: "gitea/gitea:1", Ports: web(3000), Volumes: []api.Volume{vol("data", "/data")}, Main: true,
				Env: env(
					"GITEA__database__DB_TYPE", "postgres",
					"GITEA__database__HOST", "${POSTGRES_HOST}:${POSTGRES_PORT}",
					"GITEA__database__NAME", "${POSTGRES_DATABASE}",
					"GITEA__database__USER", "${POSTGRES_USERNAME}",
					"GITEA__database__PASSWD", "${POSTGRES_PASSWORD}",
				),
				DomainEnv: env("GITEA__server__ROOT_URL", "${ATOLL_WEB_URL}/")},
		},
	},
	{
		ID: "n8n", Name: "n8n", Category: "dev", Color: "#ea4b71", Website: "https://n8n.io",
		Description: "可视化工作流自动化，连接几百种服务和 AI", DescriptionEn: "Visual workflow automation connecting hundreds of services and AI",
		NeedsDomain: true, MinMemMB: 512,
		Note: "第一次打开时创建所有者账号。", NoteEn: "Create the owner account on first visit.",
		Services: []api.AppTemplateService{
			{Name: "n8n", Image: "n8nio/n8n:latest", Ports: web(5678), Volumes: []api.Volume{{Name: "data", MountPath: "/home/node/.n8n", Owner: "1000:1000"}}, Main: true,
				Env: env("N8N_ENCRYPTION_KEY", RandomPlaceholder, "N8N_PROXY_HOPS", "1", "GENERIC_TIMEZONE", "Asia/Shanghai"),
				DomainEnv: env("N8N_HOST", "${ATOLL_WEB_DOMAIN}", "N8N_PROTOCOL", "https", "WEBHOOK_URL", "${ATOLL_WEB_URL}/",
					"N8N_EDITOR_BASE_URL", "${ATOLL_WEB_URL}/")},
		},
	},
	{
		ID: "open-webui", Name: "Open WebUI", Category: "ai", Color: "#111827", Website: "https://openwebui.com",
		Description: "类 ChatGPT 的 AI 聊天界面，接 OpenAI 兼容 API 或 Ollama", DescriptionEn: "ChatGPT-style AI chat UI for OpenAI-compatible APIs or Ollama",
		MinMemMB: 2048,
		Note:     "第一个注册的账号是管理员；在设置 → 连接里填 API 地址和密钥。至少需要 2 GB 内存。", NoteEn: "The first account to sign up is the admin; add your API URL and key under Settings → Connections. Needs at least 2 GB of memory.",
		Services: []api.AppTemplateService{
			{Name: "open-webui", Image: "ghcr.io/open-webui/open-webui:main", Ports: web(8080), Volumes: []api.Volume{vol("data", "/app/backend/data")}, Main: true, MemLimitMB: 2048,
				Env: env("WEBUI_SECRET_KEY", RandomPlaceholder)},
		},
	},
	{
		ID: "cliproxyapi", Name: "CLIProxyAPI + CPA Manager Plus", Category: "ai", Color: "#2563eb", Website: "https://github.com/router-for-me/CLIProxyAPI",
		Description: "把 Gemini CLI / Codex / Claude Code 等账号转成 OpenAI 兼容 API，附带 CPA Manager Plus 管理面板（用量统计、账号管理）", DescriptionEn: "Turns Gemini CLI / Codex / Claude Code and other accounts into an OpenAI-compatible API, with the CPA Manager Plus panel (usage analytics, account management)",
		MinMemMB: 512,
		Inputs:   []api.AppTemplateInput{secretInput("CPA_MANAGEMENT_KEY", "CPA 管理密钥", "CPA management key")},
		Note: "主域名绑定在管理面板 cpa-manager-plus 上，管理员密钥是它的环境变量 CPA_MANAGER_ADMIN_KEY；面板已通过内网 http://cli-proxy-api:8317 连好 CPA，CPA 管理密钥是创建时填写的那个（留空则自动生成，在 cli-proxy-api 的环境变量 MANAGEMENT_PASSWORD 里）。" +
			"CPA 的配置文件是 /CLIProxyAPI/conf/config.yaml（在 cli-proxy-api 的持久卷 config 上），客户端 API Key 在其中的 access.api-keys；可直接在 CPA 管理面板里改（API Key、提供商等），也可在「文件」页编辑，重新部署后保留。/CLIProxyAPI/config.seed.yaml 只是首次启动时复制的初始模板。" +
			"创建时可填「API 域名」给 cli-proxy-api 绑定域名（端口 api），供外部客户端直接调用；以后也可再绑定。",
		NoteEn: "The main domain goes to the cpa-manager-plus panel; its admin key is the CPA_MANAGER_ADMIN_KEY env var. The panel is already connected to CPA over the internal network at http://cli-proxy-api:8317; the CPA management key is the one you entered at creation (or, if left empty, the generated one in cli-proxy-api's MANAGEMENT_PASSWORD env var). " +
			"CPA's config is /CLIProxyAPI/conf/config.yaml (on cli-proxy-api's persistent volume config), with client API keys under access.api-keys. Edit it in the CPA management panel (API keys, providers, ...) or on the Files page; changes survive redeploys. /CLIProxyAPI/config.seed.yaml is only the initial copy used on first start. " +
			"Fill \"API domain\" at creation to bind a domain to cli-proxy-api (port api) for external clients, or bind one later.",
		Services: []api.AppTemplateService{
			{Name: "cli-proxy-api", Image: "eceasy/cli-proxy-api:latest", Ports: []api.Port{{Name: "api", ContainerPort: 8317, Protocol: "http"}}, MemLimitMB: 512,
				Volumes: []api.Volume{vol("auths", "/root/.cli-proxy-api"), vol("logs", "/CLIProxyAPI/logs"), vol("config", "/CLIProxyAPI/conf")},
				Configs: cfg("/CLIProxyAPI/config.seed.yaml", cpaConfig),
				// The image is debian:bookworm with the binary at /CLIProxyAPI/CLIProxyAPI (workdir).
				Command: []string{"sh", "-c", "[ -f /CLIProxyAPI/conf/config.yaml ] || cp /CLIProxyAPI/config.seed.yaml /CLIProxyAPI/conf/config.yaml; exec /CLIProxyAPI/CLIProxyAPI -config /CLIProxyAPI/conf/config.yaml"},
				Domain:  true, DomainPort: "api", DomainLabel: "API 域名（可选）", DomainLabelEn: "API domain (optional)",
				DomainHint: "给 cli-proxy-api 绑定，供客户端直接调用；长请求建议灰云（不走 Cloudflare 代理）", DomainHintEn: "Bound to cli-proxy-api for clients; for long requests use DNS only (grey cloud)",
				Env: []api.EnvVar{{Key: "MANAGEMENT_PASSWORD", Value: InputRef("CPA_MANAGEMENT_KEY"), Secret: true, Exposed: true}}},
			{Name: "cpa-manager-plus", Image: "seakee/cpa-manager-plus:latest", Ports: web(18317), Volumes: []api.Volume{vol("data", "/data")}, Main: true,
				Env: env(
					"CPA_UPSTREAM_URL", "http://${CLI_PROXY_API_HOST}:8317",
					"CPA_MANAGEMENT_KEY", "${cli-proxy-api.MANAGEMENT_PASSWORD}",
					"CPA_MANAGER_ADMIN_KEY", RandomPlaceholder,
					"USAGE_DB_PATH", "/data/usage.sqlite",
					"CPA_MANAGER_DATA_KEY_PATH", "/data/data.key",
					"USAGE_COLLECTOR_MODE", "auto",
				)},
		},
	},
	{
		ID: "grok2api", Name: "grok2api", Category: "ai", Color: "#0a0a0a", Website: "https://github.com/chenyme/grok2api",
		Description: "把 Grok 账号转成 OpenAI 兼容 API，带管理后台和账号池，SQLite 单容器", DescriptionEn: "Turns Grok accounts into an OpenAI-compatible API with an admin panel and account pool, single container on SQLite",
		MinMemMB: 256,
		Inputs:   []api.AppTemplateInput{secretInput("ADMIN_PASSWORD", "管理员密码", "Admin password")},
		Note:     "管理员用户名 admin，初始密码是创建时填写的那个；留空则自动生成，写在配置文件 /run/grok2api/config.yaml 的 bootstrapAdmin.password（可在「文件」页查看）。登录后可修改。配置文件以只读方式挂载，要改就修改服务的配置文件（configs）再重新部署；credentialEncryptionKey 添加账号后不要再改。绑定 HTTPS 域名后建议把 auth.secureCookies 改成 true。",
		NoteEn:   "The admin username is admin; the initial password is the one you entered at creation. If left empty it is generated and written to bootstrapAdmin.password in /run/grok2api/config.yaml (viewable on the Files page). You can change it after signing in. The file is mounted read-only: change it by editing the service's config file (configs) and redeploying. Never change credentialEncryptionKey after adding accounts. With an HTTPS domain bound, set auth.secureCookies to true.",
		Services: []api.AppTemplateService{
			// The image runs as uid 10001 but Atoll's volume dirs start out owned by root.
			{Name: "grok2api", Image: "ghcr.io/chenyme/grok2api:latest", Ports: web(8000), Volumes: []api.Volume{vol("data", "/app/data")}, Main: true, MemLimitMB: 512,
				Command: []string{"sh", "-c", "chown -R 10001:10001 /app/data && exec /usr/local/bin/grok2api-entrypoint /app/grok2api --config /app/config.yaml --listen 0.0.0.0:8000"},
				Configs: cfg("/run/grok2api/config.yaml", grok2apiConfig),
				Env:     env("TZ", "Asia/Shanghai")},
		},
	},
	{
		ID: "deeix-chat", Name: "DEEIX Chat", Category: "ai", Color: "#4f46e5", Website: "https://github.com/DEEIX-AI/DEEIX-Chat",
		Description: "多模型 AI 对话系统，带管理后台、知识库和 MCP，SQLite 单容器", DescriptionEn: "Multi-model AI chat with an admin console, knowledge base and MCP, single container on SQLite",
		NeedsDomain: true, MinMemMB: 512,
		Note:   "初始管理员是 admin，密码只在第一次启动的服务日志里打印一次（搜索 bootstrap superadmin created），首次登录会要求改用户名和密码。模型和其他设置在后台里配置。",
		NoteEn: "The initial admin is admin; the password is printed once in the service logs on first start (search for \"bootstrap superadmin created\"). The first sign-in asks you to change the username and password. Models and other settings are configured in the admin console.",
		Services: []api.AppTemplateService{
			{Name: "deeix-chat", Image: "ghcr.io/deeix-ai/deeix-chat:latest", Ports: web(8080), Main: true,
				Volumes: []api.Volume{vol("data", "/app/data"), vol("storage", "/app/storage")},
				Env: env(
					"APP_ENV", "prod",
					"DATABASE_DRIVER", "sqlite",
					"SQLITE_PATH", "/app/data/deeix.db",
					"CACHE_DRIVER", "memory",
					"STORAGE_BACKEND", "local",
					"STORAGE_ROOT_DIR", "/app/storage",
					"JWT_SECRET", RandomPlaceholder,
					"DATA_ENCRYPTION_KEY", RandomPlaceholder,
					"TRUSTED_PROXIES", "127.0.0.1/32,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7",
				),
				DomainEnv: env("PUBLIC_API_BASE_URL", "${ATOLL_WEB_URL}", "PUBLIC_WEB_BASE_URL", "${ATOLL_WEB_URL}", "CORS_ALLOW_ORIGIN", "${ATOLL_WEB_URL}")},
		},
	},
}

// Apps returns the one-click apps in display order.
func Apps() []api.AppTemplate { return apps }

// App returns the app template with this id.
func App(id string) (api.AppTemplate, bool) {
	for _, a := range apps {
		if a.ID == id {
			return a, true
		}
	}
	return api.AppTemplate{}, false
}

// ResolveInputs returns the value of every input of t: the given one, or a fresh random
// secret when it is missing or empty. Unknown keys and values that would break an env
// value or a YAML string are rejected; the error never contains the value.
func ResolveInputs(t api.AppTemplate, given map[string]string) (map[string]string, error) {
	known := map[string]bool{}
	for _, in := range t.Inputs {
		known[in.Key] = true
	}
	for k := range given {
		if !known[k] {
			return nil, fmt.Errorf("unknown input %q for app %s", k, t.ID)
		}
	}
	out := make(map[string]string, len(t.Inputs))
	for _, in := range t.Inputs {
		v := given[in.Key]
		if v == "" {
			out[in.Key] = RandomPassword(32)
			continue
		}
		if err := checkInput(v); err != nil {
			return nil, fmt.Errorf("input %s: %w", in.Key, err)
		}
		out[in.Key] = v
	}
	return out, nil
}

func checkInput(v string) error {
	if len(v) < 8 || len(v) > 128 {
		return fmt.Errorf("must be 8 to 128 characters")
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return fmt.Errorf("must not contain quotes, backslashes or control characters")
		}
	}
	if strings.Contains(v, "{{") || strings.Contains(v, "${") {
		return fmt.Errorf("must not contain \"{{\" or \"${\"")
	}
	return nil
}

// Fill replaces {{INPUT:KEY}} with the resolved inputs, then fills {{RANDOM}} placeholders.
func Fill(v string, inputs map[string]string) string {
	for k, val := range inputs {
		v = strings.ReplaceAll(v, InputRef(k), val)
	}
	return FillRandom(v)
}

// FillRandom replaces every {{RANDOM}} with its own fresh secret and every
// {{RANDOM_BASE64}} with its own base64-encoded 32 random bytes.
func FillRandom(v string) string {
	for strings.Contains(v, RandomPlaceholder) {
		v = strings.Replace(v, RandomPlaceholder, RandomPassword(32), 1)
	}
	for strings.Contains(v, RandomBase64Placeholder) {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		v = strings.Replace(v, RandomBase64Placeholder, base64.StdEncoding.EncodeToString(b), 1)
	}
	return v
}
