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
      "mount_path": "/home/node/.n8n"
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
 }
]
export default apps
