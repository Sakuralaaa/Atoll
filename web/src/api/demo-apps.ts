// Generated from internal/server/templates/apps.go for demo mode (regenerate when templates change).
import type { AppTemplate } from './types'
const apps: AppTemplate[] = [
 {
  "id": "halo",
  "name": "Halo",
  "category": "blog",
  "description": "国内流行的开源建站工具，主题和插件丰富，带 PostgreSQL",
  "description_en": "Popular open-source site builder with themes and plugins, with PostgreSQL",
  "website": "https://www.halo.run",
  "color": "#0f5bf7",
  "needs_domain": false,
  "min_mem_mb": 1024,
  "note": "打开后按页面引导创建管理员账号。",
  "note_en": "Open it and follow the setup page to create the admin account.",
  "services": [
   {
    "name": "postgres",
    "template": "postgres"
   },
   {
    "name": "halo",
    "image": "halohub/halo:2",
    "ports": [
     {
      "name": "web",
      "container_port": 8090,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/root/.halo2"
     }
    ],
    "env": [
     {
      "key": "SPRING_R2DBC_URL",
      "value": "r2dbc:pool:postgresql://${POSTGRES_HOST}:${POSTGRES_PORT}/${POSTGRES_DATABASE}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "SPRING_R2DBC_USERNAME",
      "value": "${POSTGRES_USERNAME}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "SPRING_R2DBC_PASSWORD",
      "value": "${POSTGRES_PASSWORD}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "SPRING_SQL_INIT_PLATFORM",
      "value": "postgresql",
      "secret": false,
      "exposed": false
     }
    ],
    "domain_env": [
     {
      "key": "HALO_EXTERNAL_URL",
      "value": "${ATOLL_WEB_URL}",
      "secret": false,
      "exposed": false
     }
    ],
    "mem_limit_mb": 1024,
    "main": true
   }
  ]
 },
 {
  "id": "ghost",
  "name": "Ghost",
  "category": "blog",
  "description": "简洁好看的写作和会员订阅平台，带 MySQL",
  "description_en": "Clean publishing and newsletter platform, with MySQL",
  "website": "https://ghost.org",
  "color": "#15171a",
  "needs_domain": true,
  "min_mem_mb": 768,
  "note": "后台地址是 域名/ghost，第一次打开时创建管理员。",
  "note_en": "The admin is at your-domain/ghost; create the owner account on first visit.",
  "services": [
   {
    "name": "mysql",
    "template": "mysql"
   },
   {
    "name": "ghost",
    "image": "ghost:5-alpine",
    "ports": [
     {
      "name": "web",
      "container_port": 2368,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "content",
      "mount_path": "/var/lib/ghost/content"
     }
    ],
    "env": [
     {
      "key": "database__client",
      "value": "mysql",
      "secret": false,
      "exposed": false
     },
     {
      "key": "database__connection__host",
      "value": "${MYSQL_HOST}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "database__connection__port",
      "value": "${MYSQL_PORT}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "database__connection__user",
      "value": "${MYSQL_USERNAME}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "database__connection__password",
      "value": "${MYSQL_PASSWORD}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "database__connection__database",
      "value": "${MYSQL_DATABASE}",
      "secret": false,
      "exposed": false
     }
    ],
    "domain_env": [
     {
      "key": "url",
      "value": "${ATOLL_WEB_URL}",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "papergrid",
  "name": "PaperGrid",
  "category": "blog",
  "description": "Atoll 风格的轻量博客，SQLite 单容器，内置 MCP 让 AI Agent 写文章",
  "description_en": "Lightweight Atoll-styled blog on SQLite with a built-in MCP endpoint for AI agents",
  "website": "https://github.com/Sakuralaaa/blog",
  "color": "#8b7cf6",
  "needs_domain": true,
  "min_mem_mb": 512,
  "note": "管理员邮箱 admin@example.com，初始密码在环境变量 ADMIN_INIT_PASSWORD，登录后在后台修改。后台「接口密钥」页有 MCP 接入说明。",
  "note_en": "Admin email is admin@example.com; the initial password is in the ADMIN_INIT_PASSWORD env var. Change it after signing in. The API Keys page explains MCP setup.",
  "services": [
   {
    "name": "papergrid",
    "image": "ghcr.io/sakuralaaa/blog:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 3000,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/data"
     }
    ],
    "env": [
     {
      "key": "DATABASE_URL",
      "value": "file:/data/db.sqlite",
      "secret": false,
      "exposed": false
     },
     {
      "key": "NEXTAUTH_SECRET",
      "value": "{{RANDOM}}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "ADMIN_INIT_PASSWORD",
      "value": "{{RANDOM}}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "AUTH_TRUST_HOST",
      "value": "1",
      "secret": false,
      "exposed": false
     },
     {
      "key": "NEXT_CACHE_DIR",
      "value": "/data/.next-cache",
      "secret": false,
      "exposed": false
     },
     {
      "key": "MEDIA_ROOT",
      "value": "/data/uploads",
      "secret": false,
      "exposed": false
     }
    ],
    "domain_env": [
     {
      "key": "NEXTAUTH_URL",
      "value": "${ATOLL_WEB_URL}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "NEXT_PUBLIC_APP_URL",
      "value": "${ATOLL_WEB_URL}",
      "secret": false,
      "exposed": false
     }
    ],
    "mem_limit_mb": 512,
    "main": true
   }
  ]
 },
 {
  "id": "wordpress",
  "name": "WordPress",
  "category": "blog",
  "description": "全世界用得最多的博客和建站系统，带 MySQL",
  "description_en": "The world's most used blogging and site system, with MySQL",
  "website": "https://wordpress.org",
  "color": "#21759b",
  "needs_domain": false,
  "min_mem_mb": 768,
  "note": "打开后按安装向导设置站点和管理员。",
  "note_en": "Open it and run the install wizard to set up the site and admin.",
  "services": [
   {
    "name": "mysql",
    "template": "mysql"
   },
   {
    "name": "wordpress",
    "image": "wordpress:6-apache",
    "ports": [
     {
      "name": "web",
      "container_port": 80,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "html",
      "mount_path": "/var/www/html"
     }
    ],
    "env": [
     {
      "key": "WORDPRESS_DB_HOST",
      "value": "${MYSQL_HOST}:${MYSQL_PORT}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "WORDPRESS_DB_USER",
      "value": "${MYSQL_USERNAME}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "WORDPRESS_DB_PASSWORD",
      "value": "${MYSQL_PASSWORD}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "WORDPRESS_DB_NAME",
      "value": "${MYSQL_DATABASE}",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "memos",
  "name": "Memos",
  "category": "tools",
  "description": "轻量的碎碎念和笔记，像自己的私人微博",
  "description_en": "Lightweight notes and memos, like a private microblog",
  "website": "https://usememos.com",
  "color": "#f59e0b",
  "needs_domain": false,
  "min_mem_mb": 128,
  "note": "第一个注册的账号是管理员。",
  "note_en": "The first account to sign up becomes the admin.",
  "services": [
   {
    "name": "memos",
    "image": "neosmemo/memos:stable",
    "ports": [
     {
      "name": "web",
      "container_port": 5230,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/var/opt/memos"
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "alist",
  "name": "AList",
  "category": "tools",
  "description": "把各种网盘和存储挂成一个文件列表，支持在线预览",
  "description_en": "Mounts cloud drives and storage into one file list with previews",
  "website": "https://alistgo.com",
  "color": "#1890ff",
  "needs_domain": false,
  "min_mem_mb": 128,
  "note": "初始管理员密码在服务日志里（搜索 password）。",
  "note_en": "The initial admin password is in the service logs (search for \"password\").",
  "services": [
   {
    "name": "alist",
    "image": "xhofe/alist:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 5244,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/opt/alist/data"
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "vaultwarden",
  "name": "Vaultwarden",
  "category": "tools",
  "description": "兼容 Bitwarden 客户端的密码管理器服务端",
  "description_en": "Password manager server compatible with Bitwarden clients",
  "website": "https://github.com/dani-garcia/vaultwarden",
  "color": "#175ddc",
  "needs_domain": true,
  "min_mem_mb": 128,
  "note": "网页端必须通过 HTTPS 访问。注册完自己的账号后，建议把 SIGNUPS_ALLOWED 改成 false。",
  "note_en": "The web vault needs HTTPS. After creating your account, set SIGNUPS_ALLOWED to false.",
  "services": [
   {
    "name": "vaultwarden",
    "image": "vaultwarden/server:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 80,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/data"
     }
    ],
    "env": [
     {
      "key": "SIGNUPS_ALLOWED",
      "value": "true",
      "secret": false,
      "exposed": false
     }
    ],
    "domain_env": [
     {
      "key": "DOMAIN",
      "value": "${ATOLL_WEB_URL}",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "freshrss",
  "name": "FreshRSS",
  "category": "tools",
  "description": "自托管 RSS 阅读器，支持 Fever / Google Reader API 客户端",
  "description_en": "Self-hosted RSS reader with Fever / Google Reader API support",
  "website": "https://freshrss.org",
  "color": "#0062be",
  "needs_domain": false,
  "min_mem_mb": 128,
  "services": [
   {
    "name": "freshrss",
    "image": "freshrss/freshrss:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 80,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/var/www/FreshRSS/data"
     },
     {
      "name": "extensions",
      "mount_path": "/var/www/FreshRSS/extensions"
     }
    ],
    "env": [
     {
      "key": "TZ",
      "value": "Asia/Shanghai",
      "secret": false,
      "exposed": false
     },
     {
      "key": "CRON_MIN",
      "value": "*/20",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "it-tools",
  "name": "IT Tools",
  "category": "tools",
  "description": "开发常用小工具合集：编码转换、哈希、UUID、JSON 格式化等",
  "description_en": "Handy developer tools: encoders, hashes, UUIDs, JSON formatting and more",
  "website": "https://it-tools.tech",
  "color": "#18a058",
  "needs_domain": false,
  "min_mem_mb": 64,
  "services": [
   {
    "name": "it-tools",
    "image": "corentinth/it-tools:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 80,
      "protocol": "http"
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "excalidraw",
  "name": "Excalidraw",
  "category": "tools",
  "description": "手绘风格的在线白板，画草图和流程图",
  "description_en": "Hand-drawn style whiteboard for sketches and diagrams",
  "website": "https://excalidraw.com",
  "color": "#6965db",
  "needs_domain": false,
  "min_mem_mb": 64,
  "note": "画布保存在浏览器本地。",
  "note_en": "Drawings are stored in your browser.",
  "services": [
   {
    "name": "excalidraw",
    "image": "excalidraw/excalidraw:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 80,
      "protocol": "http"
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "flaresolverr",
  "name": "FlareSolverr",
  "category": "tools",
  "description": "用无头 Chrome 过 Cloudflare 验证的代理接口，给 grok2api、Jackett 等程序调用",
  "description_en": "Proxy API that passes Cloudflare challenges with headless Chrome, for grok2api, Jackett and similar tools",
  "website": "https://github.com/FlareSolverr/FlareSolverr",
  "color": "#f38020",
  "needs_domain": false,
  "min_mem_mb": 512,
  "note": "只有 API，没有网页界面：程序调用 POST /v1。同一项目里的服务用 http://flaresolverr:8191 访问；其他项目要用就绑定域名。浏览器很吃内存，内存小的机器别开太多会话。",
  "note_en": "API only, no web UI: apps call POST /v1. Services in the same project reach it at http://flaresolverr:8191; bind a domain to use it from other projects. Browsers use a lot of memory, so keep sessions few on small servers.",
  "services": [
   {
    "name": "flaresolverr",
    "image": "ghcr.io/flaresolverr/flaresolverr:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 8191,
      "protocol": "http"
     }
    ],
    "env": [
     {
      "key": "LOG_LEVEL",
      "value": "info",
      "secret": false,
      "exposed": false
     },
     {
      "key": "TZ",
      "value": "Asia/Shanghai",
      "secret": false,
      "exposed": false
     }
    ],
    "mem_limit_mb": 1024,
    "main": true
   }
  ]
 },
 {
  "id": "outlook-email",
  "name": "OutlookEmail",
  "category": "tools",
  "description": "批量管理 Outlook / Hotmail 邮箱，网页里收信看验证码",
  "description_en": "Manage many Outlook / Hotmail mailboxes and read mail and verification codes in the browser",
  "website": "https://github.com/assast/outlookEmail",
  "color": "#0078d4",
  "needs_domain": false,
  "min_mem_mb": 256,
  "note": "登录密码是创建时填写的那个；留空则自动生成，在环境变量 LOGIN_PASSWORD 里（只在第一次启动时生效，之后在网页设置里改）。SECRET_KEY 用来加密数据，不要改。镜像自带的「一键更新」已关闭，更新请在 Atoll 里重新部署。",
  "note_en": "The login password is the one you entered at creation; if left empty it is generated and kept in the LOGIN_PASSWORD env var (only used on first start; change it later in the web settings). SECRET_KEY encrypts the data, so don't change it. The image's built-in self-update is disabled; update by redeploying in Atoll.",
  "inputs": [
   {
    "key": "LOGIN_PASSWORD",
    "label": "登录密码",
    "label_en": "Login password",
    "secret": true
   }
  ],
  "services": [
   {
    "name": "outlook-email",
    "image": "ghcr.io/assast/outlookemail:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 5000,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/app/data"
     }
    ],
    "env": [
     {
      "key": "LOGIN_PASSWORD",
      "value": "{{INPUT:LOGIN_PASSWORD}}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "SECRET_KEY",
      "value": "{{RANDOM}}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "FLASK_ENV",
      "value": "production",
      "secret": false,
      "exposed": false
     },
     {
      "key": "DOCKER_UPDATE_ENABLED",
      "value": "false",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "uptime-kuma",
  "name": "Uptime Kuma",
  "category": "ops",
  "description": "好看的网站和服务可用性监控，支持多种通知和状态页",
  "description_en": "Good-looking uptime monitoring with notifications and status pages",
  "website": "https://uptime.kuma.pet",
  "color": "#5cdd8b",
  "needs_domain": false,
  "min_mem_mb": 256,
  "note": "第一次打开时创建管理员账号。",
  "note_en": "Create the admin account on first visit.",
  "services": [
   {
    "name": "uptime-kuma",
    "image": "louislam/uptime-kuma:1",
    "ports": [
     {
      "name": "web",
      "container_port": 3001,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/app/data"
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "umami",
  "name": "Umami",
  "category": "ops",
  "description": "注重隐私的网站访问统计，Google Analytics 的轻量替代，带 PostgreSQL",
  "description_en": "Privacy-focused web analytics, a light Google Analytics alternative, with PostgreSQL",
  "website": "https://umami.is",
  "color": "#000000",
  "needs_domain": false,
  "min_mem_mb": 512,
  "note": "默认账号 admin / umami，登录后立刻改密码。",
  "note_en": "Default login is admin / umami; change the password right away.",
  "services": [
   {
    "name": "postgres",
    "template": "postgres"
   },
   {
    "name": "umami",
    "image": "ghcr.io/umami-software/umami:postgresql-latest",
    "ports": [
     {
      "name": "web",
      "container_port": 3000,
      "protocol": "http"
     }
    ],
    "env": [
     {
      "key": "DATABASE_URL",
      "value": "${POSTGRES_CONNECTION_STRING}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "APP_SECRET",
      "value": "{{RANDOM}}",
      "secret": true,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "gitea",
  "name": "Gitea",
  "category": "dev",
  "description": "轻量的自托管 Git 服务，带 PostgreSQL",
  "description_en": "Lightweight self-hosted Git service, with PostgreSQL",
  "website": "https://about.gitea.com",
  "color": "#609926",
  "needs_domain": false,
  "min_mem_mb": 512,
  "note": "第一次打开是安装页，数据库已经填好，直接创建管理员即可。只开放了 HTTP，Git 走 HTTPS 克隆。",
  "note_en": "The first visit is the install page with the database prefilled; just create the admin. Only HTTP is exposed, so clone over HTTPS.",
  "services": [
   {
    "name": "postgres",
    "template": "postgres"
   },
   {
    "name": "gitea",
    "image": "gitea/gitea:1",
    "ports": [
     {
      "name": "web",
      "container_port": 3000,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/data"
     }
    ],
    "env": [
     {
      "key": "GITEA__database__DB_TYPE",
      "value": "postgres",
      "secret": false,
      "exposed": false
     },
     {
      "key": "GITEA__database__HOST",
      "value": "${POSTGRES_HOST}:${POSTGRES_PORT}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "GITEA__database__NAME",
      "value": "${POSTGRES_DATABASE}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "GITEA__database__USER",
      "value": "${POSTGRES_USERNAME}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "GITEA__database__PASSWD",
      "value": "${POSTGRES_PASSWORD}",
      "secret": true,
      "exposed": false
     }
    ],
    "domain_env": [
     {
      "key": "GITEA__server__ROOT_URL",
      "value": "${ATOLL_WEB_URL}/",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "n8n",
  "name": "n8n",
  "category": "dev",
  "description": "可视化工作流自动化，连接几百种服务和 AI",
  "description_en": "Visual workflow automation connecting hundreds of services and AI",
  "website": "https://n8n.io",
  "color": "#ea4b71",
  "needs_domain": true,
  "min_mem_mb": 512,
  "note": "第一次打开时创建所有者账号。",
  "note_en": "Create the owner account on first visit.",
  "services": [
   {
    "name": "n8n",
    "image": "n8nio/n8n:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 5678,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/home/node/.n8n",
      "owner": "1000:1000"
     }
    ],
    "env": [
     {
      "key": "N8N_ENCRYPTION_KEY",
      "value": "{{RANDOM}}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "N8N_PROXY_HOPS",
      "value": "1",
      "secret": false,
      "exposed": false
     },
     {
      "key": "GENERIC_TIMEZONE",
      "value": "Asia/Shanghai",
      "secret": false,
      "exposed": false
     }
    ],
    "domain_env": [
     {
      "key": "N8N_HOST",
      "value": "${ATOLL_WEB_DOMAIN}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "N8N_PROTOCOL",
      "value": "https",
      "secret": false,
      "exposed": false
     },
     {
      "key": "WEBHOOK_URL",
      "value": "${ATOLL_WEB_URL}/",
      "secret": false,
      "exposed": false
     },
     {
      "key": "N8N_EDITOR_BASE_URL",
      "value": "${ATOLL_WEB_URL}/",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "open-webui",
  "name": "Open WebUI",
  "category": "ai",
  "description": "类 ChatGPT 的 AI 聊天界面，接 OpenAI 兼容 API 或 Ollama",
  "description_en": "ChatGPT-style AI chat UI for OpenAI-compatible APIs or Ollama",
  "website": "https://openwebui.com",
  "color": "#111827",
  "needs_domain": false,
  "min_mem_mb": 2048,
  "note": "第一个注册的账号是管理员；在设置 → 连接里填 API 地址和密钥。至少需要 2 GB 内存。",
  "note_en": "The first account to sign up is the admin; add your API URL and key under Settings → Connections. Needs at least 2 GB of memory.",
  "services": [
   {
    "name": "open-webui",
    "image": "ghcr.io/open-webui/open-webui:main",
    "ports": [
     {
      "name": "web",
      "container_port": 8080,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/app/backend/data"
     }
    ],
    "env": [
     {
      "key": "WEBUI_SECRET_KEY",
      "value": "{{RANDOM}}",
      "secret": true,
      "exposed": false
     }
    ],
    "mem_limit_mb": 2048,
    "main": true
   }
  ]
 },
 {
  "id": "cliproxyapi",
  "name": "CLIProxyAPI + CPA Manager Plus",
  "category": "ai",
  "description": "把 Gemini CLI / Codex / Claude Code 等账号转成 OpenAI 兼容 API，附带 CPA Manager Plus 管理面板（用量统计、账号管理）",
  "description_en": "Turns Gemini CLI / Codex / Claude Code and other accounts into an OpenAI-compatible API, with the CPA Manager Plus panel (usage analytics, account management)",
  "website": "https://github.com/router-for-me/CLIProxyAPI",
  "color": "#2563eb",
  "needs_domain": false,
  "min_mem_mb": 512,
  "note": "主域名绑定在管理面板 cpa-manager-plus 上，登录密钥和 CPA 管理密钥相同；面板已通过内网 http://cli-proxy-api:8317 连好 CPA，CPA 管理密钥是创建时填写的那个（留空则自动生成，在 cli-proxy-api 的环境变量 MANAGEMENT_PASSWORD 里）。CPA 的配置文件是 /CLIProxyAPI/conf/config.yaml（在 cli-proxy-api 的持久卷 config 上），客户端 API Key 在其中的 access.api-keys；可直接在 CPA 管理面板里改（API Key、提供商等），也可在「文件」页编辑，重新部署后保留。/CLIProxyAPI/config.seed.yaml 只是首次启动时复制的初始模板。创建时可填「API 域名」给 cli-proxy-api 绑定域名（端口 api），供外部客户端直接调用；以后也可再绑定。",
  "note_en": "The main domain goes to the cpa-manager-plus panel; its login key is the same as the CPA management key. The panel is already connected to CPA over the internal network at http://cli-proxy-api:8317; the CPA management key is the one you entered at creation (or, if left empty, the generated one in cli-proxy-api's MANAGEMENT_PASSWORD env var). CPA's config is /CLIProxyAPI/conf/config.yaml (on cli-proxy-api's persistent volume config), with client API keys under access.api-keys. Edit it in the CPA management panel (API keys, providers, ...) or on the Files page; changes survive redeploys. /CLIProxyAPI/config.seed.yaml is only the initial copy used on first start. Fill \"API domain\" at creation to bind a domain to cli-proxy-api (port api) for external clients, or bind one later.",
  "inputs": [
   {
    "key": "CPA_MANAGEMENT_KEY",
    "label": "CPA 管理密钥",
    "label_en": "CPA management key",
    "secret": true
   }
  ],
  "services": [
   {
    "name": "cli-proxy-api",
    "image": "eceasy/cli-proxy-api:latest",
    "command": [
     "sh",
     "-c",
     "[ -f /CLIProxyAPI/conf/config.yaml ] || cp /CLIProxyAPI/config.seed.yaml /CLIProxyAPI/conf/config.yaml; exec /CLIProxyAPI/CLIProxyAPI -config /CLIProxyAPI/conf/config.yaml"
    ],
    "ports": [
     {
      "name": "api",
      "container_port": 8317,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "auths",
      "mount_path": "/root/.cli-proxy-api"
     },
     {
      "name": "logs",
      "mount_path": "/CLIProxyAPI/logs"
     },
     {
      "name": "config",
      "mount_path": "/CLIProxyAPI/conf"
     }
    ],
    "configs": [
     {
      "path": "/CLIProxyAPI/config.seed.yaml",
      "content": "# CLIProxyAPI config (Atoll one-click app). Full reference: config.example.yaml in\n# github.com/router-for-me/CLIProxyAPI. The live copy is /CLIProxyAPI/conf/config.yaml (on the\n# \"config\" volume): edit it in the CPA panel or on Atoll's Files page; it survives redeploys.\n# This seed is only copied when that file does not exist yet.\nconfig-version: 8\n\nserver:\n  port: 8317\n\nmanagement:\n  # The management key is the MANAGEMENT_PASSWORD env var of this service (set at creation).\n  allow-remote: true\n  secret-key: \"\"\n\naccess:\n  # Keys your clients send as \"Authorization: Bearer \u003ckey\u003e\".\n  api-keys:\n    - \"sk-{{RANDOM}}\"\n\noauth:\n  auth-dir: \"/root/.cli-proxy-api\"\n\nobservability:\n  usage:\n    # Needed for request monitoring in CPA Manager Plus.\n    usage-statistics-enabled: true\n    redis-usage-queue-retention-seconds: 60\n"
     }
    ],
    "env": [
     {
      "key": "MANAGEMENT_PASSWORD",
      "value": "{{INPUT:CPA_MANAGEMENT_KEY}}",
      "secret": true,
      "exposed": true
     }
    ],
    "mem_limit_mb": 512,
    "domain": true,
    "domain_port": "api",
    "domain_label": "API 域名（可选）",
    "domain_label_en": "API domain (optional)",
    "domain_hint": "给 cli-proxy-api 绑定，供客户端直接调用；长请求建议灰云（不走 Cloudflare 代理）",
    "domain_hint_en": "Bound to cli-proxy-api for clients; for long requests use DNS only (grey cloud)"
   },
   {
    "name": "cpa-manager-plus",
    "image": "seakee/cpa-manager-plus:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 18317,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/data"
     }
    ],
    "env": [
     {
      "key": "CPA_UPSTREAM_URL",
      "value": "http://${CLI_PROXY_API_HOST}:8317",
      "secret": false,
      "exposed": false
     },
     {
      "key": "CPA_MANAGEMENT_KEY",
      "value": "${cli-proxy-api.MANAGEMENT_PASSWORD}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "CPA_MANAGER_ADMIN_KEY",
      "value": "${cli-proxy-api.MANAGEMENT_PASSWORD}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "USAGE_DB_PATH",
      "value": "/data/usage.sqlite",
      "secret": false,
      "exposed": false
     },
     {
      "key": "CPA_MANAGER_DATA_KEY_PATH",
      "value": "/data/data.key",
      "secret": false,
      "exposed": false
     },
     {
      "key": "USAGE_COLLECTOR_MODE",
      "value": "auto",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 },
 {
  "id": "grok2api",
  "name": "grok2api",
  "category": "ai",
  "description": "把 Grok 账号转成 OpenAI 兼容 API，带管理后台和账号池，SQLite 单容器",
  "description_en": "Turns Grok accounts into an OpenAI-compatible API with an admin panel and account pool, single container on SQLite",
  "website": "https://github.com/chenyme/grok2api",
  "color": "#0a0a0a",
  "needs_domain": false,
  "min_mem_mb": 256,
  "note": "管理员用户名 admin，初始密码是创建时填写的那个；留空则自动生成，写在配置文件 /run/grok2api/config.yaml 的 bootstrapAdmin.password（可在「文件」页查看）。登录后可修改。配置文件以只读方式挂载，要改就修改服务的配置文件（configs）再重新部署；credentialEncryptionKey 添加账号后不要再改。绑定 HTTPS 域名后建议把 auth.secureCookies 改成 true。",
  "note_en": "The admin username is admin; the initial password is the one you entered at creation. If left empty it is generated and written to bootstrapAdmin.password in /run/grok2api/config.yaml (viewable on the Files page). You can change it after signing in. The file is mounted read-only: change it by editing the service's config file (configs) and redeploying. Never change credentialEncryptionKey after adding accounts. With an HTTPS domain bound, set auth.secureCookies to true.",
  "inputs": [
   {
    "key": "ADMIN_PASSWORD",
    "label": "管理员密码",
    "label_en": "Admin password",
    "secret": true
   }
  ],
  "services": [
   {
    "name": "grok2api",
    "image": "ghcr.io/chenyme/grok2api:latest",
    "command": [
     "sh",
     "-c",
     "chown -R 10001:10001 /app/data \u0026\u0026 exec /usr/local/bin/grok2api-entrypoint /app/grok2api --config /app/config.yaml --listen 0.0.0.0:8000"
    ],
    "ports": [
     {
      "name": "web",
      "container_port": 8000,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/app/data"
     }
    ],
    "configs": [
     {
      "path": "/run/grok2api/config.yaml",
      "content": "# grok2api config (Atoll one-click app). Full reference: config.example.yaml in\n# github.com/chenyme/grok2api. Edit it in the service's configs, then redeploy.\nserver:\n  listen: \"0.0.0.0:8000\"\n\nauth:\n  # Set to true once the panel is only opened over HTTPS (a bound domain).\n  secureCookies: false\n\nsecrets:\n  jwtSecret: \"{{RANDOM}}\"\n  # Keep this forever once accounts are added: changing it makes stored credentials unreadable.\n  credentialEncryptionKey: \"{{RANDOM_BASE64}}\"\n\nbootstrapAdmin:\n  # Used only while the database has no admin. Change the password after signing in.\n  username: \"admin\"\n  password: \"{{INPUT:ADMIN_PASSWORD}}\"\n"
     }
    ],
    "env": [
     {
      "key": "TZ",
      "value": "Asia/Shanghai",
      "secret": false,
      "exposed": false
     }
    ],
    "mem_limit_mb": 512,
    "main": true
   }
  ]
 },
 {
  "id": "deeix-chat",
  "name": "DEEIX Chat",
  "category": "ai",
  "description": "多模型 AI 对话系统，带管理后台、知识库和 MCP，SQLite 单容器",
  "description_en": "Multi-model AI chat with an admin console, knowledge base and MCP, single container on SQLite",
  "website": "https://github.com/DEEIX-AI/DEEIX-Chat",
  "color": "#4f46e5",
  "needs_domain": true,
  "min_mem_mb": 512,
  "note": "初始管理员是 admin，密码只在第一次启动的服务日志里打印一次（搜索 bootstrap superadmin created），首次登录会要求改用户名和密码。模型和其他设置在后台里配置。",
  "note_en": "The initial admin is admin; the password is printed once in the service logs on first start (search for \"bootstrap superadmin created\"). The first sign-in asks you to change the username and password. Models and other settings are configured in the admin console.",
  "services": [
   {
    "name": "deeix-chat",
    "image": "ghcr.io/deeix-ai/deeix-chat:latest",
    "ports": [
     {
      "name": "web",
      "container_port": 8080,
      "protocol": "http"
     }
    ],
    "volumes": [
     {
      "name": "data",
      "mount_path": "/app/data"
     },
     {
      "name": "storage",
      "mount_path": "/app/storage"
     }
    ],
    "env": [
     {
      "key": "APP_ENV",
      "value": "prod",
      "secret": false,
      "exposed": false
     },
     {
      "key": "DATABASE_DRIVER",
      "value": "sqlite",
      "secret": false,
      "exposed": false
     },
     {
      "key": "SQLITE_PATH",
      "value": "/app/data/deeix.db",
      "secret": false,
      "exposed": false
     },
     {
      "key": "CACHE_DRIVER",
      "value": "memory",
      "secret": false,
      "exposed": false
     },
     {
      "key": "STORAGE_BACKEND",
      "value": "local",
      "secret": false,
      "exposed": false
     },
     {
      "key": "STORAGE_ROOT_DIR",
      "value": "/app/storage",
      "secret": false,
      "exposed": false
     },
     {
      "key": "JWT_SECRET",
      "value": "{{RANDOM}}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "DATA_ENCRYPTION_KEY",
      "value": "{{RANDOM}}",
      "secret": true,
      "exposed": false
     },
     {
      "key": "TRUSTED_PROXIES",
      "value": "127.0.0.1/32,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7",
      "secret": false,
      "exposed": false
     }
    ],
    "domain_env": [
     {
      "key": "PUBLIC_API_BASE_URL",
      "value": "${ATOLL_WEB_URL}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "PUBLIC_WEB_BASE_URL",
      "value": "${ATOLL_WEB_URL}",
      "secret": false,
      "exposed": false
     },
     {
      "key": "CORS_ALLOW_ORIGIN",
      "value": "${ATOLL_WEB_URL}",
      "secret": false,
      "exposed": false
     }
    ],
    "main": true
   }
  ]
 }
]
export default apps
