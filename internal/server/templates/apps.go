package templates

import (
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// RandomPlaceholder in an app template env value becomes a fresh random secret.
const RandomPlaceholder = "{{RANDOM}}"

func web(port int) []api.Port {
	return []api.Port{{Name: "web", ContainerPort: port, Protocol: "http"}}
}

func vol(name, path string) api.Volume { return api.Volume{Name: name, MountPath: path} }

func env(kv ...string) []api.EnvVar {
	out := make([]api.EnvVar, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		v := kv[i+1]
		out = append(out, api.EnvVar{Key: kv[i], Value: v, Secret: strings.Contains(v, RandomPlaceholder) || strings.Contains(v, "PASSWORD}") || strings.Contains(v, "CONNECTION_STRING}")})
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
			{Name: "n8n", Image: "n8nio/n8n:latest", Ports: web(5678), Volumes: []api.Volume{vol("data", "/home/node/.n8n")}, Main: true,
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

// FillRandom replaces every {{RANDOM}} with its own fresh secret.
func FillRandom(v string) string {
	for strings.Contains(v, RandomPlaceholder) {
		v = strings.Replace(v, RandomPlaceholder, RandomPassword(32), 1)
	}
	return v
}
