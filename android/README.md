# Atoll Android 客户端

Atoll 面板的原生 Android 壳应用：原生登录，然后全屏打开你的面板。Kotlin + Jetpack Compose + Material 3，Android 8.0（API 26）及以上，包名 `io.atoll.app`。

## 下载

- 正式版：每个版本的 [GitHub Release](https://github.com/Sakuralaaa/Atoll/releases) 附件 `atoll-android-<版本>.apk`，旁边的 `.sha256` 可用来校验：`sha256sum -c atoll-android-<版本>.apk.sha256`。
- 开发版：`main` 上改动 `android/` 时，[android 工作流](https://github.com/Sakuralaaa/Atoll/actions/workflows/android.yml) 会构建 `atoll-android-0.1.31-dev.<运行号>` artifact。

所有 APK 用同一个发布密钥签名，新版本可以直接覆盖安装。

## 功能

| 功能 | 说明 |
| --- | --- |
| 面板列表 | 保存多个面板（名称、地址、用户名），添加 / 编辑 / 删除；首次启动直接进入添加页 |
| 原生登录 | 地址自动补 `https://` 并规范为 `scheme://host[:port]`，用 `GET /api/v1/auth/status` 验证；用户名 + 密码，两步验证时输入 6 位验证码或恢复码；中文错误提示（密码错误、连不上、证书错误、限流等） |
| 凭据保存 | 「记住密码」开启时用 EncryptedSharedPreferences（Android Keystore 密钥）加密保存，不参与云备份 / 设备迁移；可选打开前验证指纹 / 面容 / 锁屏密码 |
| 面板 WebView | 登录后把 `atoll_session` Cookie 写入 CookieManager 再打开面板；JS、DOM Storage、持久 Cookie；下拉刷新（只在内容滚到顶部时）；返回键先回退页面，再回到列表 |
| 上传 / 下载 | `<input type=file>` 调起系统文件选择器（支持多选）；普通下载交给 DownloadManager 并带上 Cookie；页面生成的文件（blob: / data:，如导出 .env、恢复码、日志）直接保存到「下载」 |
| 外部链接 | 其他域名的链接用 Custom Tabs / 浏览器打开，mailto: 等交给对应应用 |
| 出错处理 | 加载进度条；离线、域名解析失败、连接失败、证书错误、5xx 时显示提示页和「重试」 |
| 外观 | 全面屏（edge-to-edge），状态栏 / 导航栏颜色读取面板当前的 `--bg` 并跟随深浅主题；启动画面和自适应图标（含 Android 13 单色图标）使用 Atoll 标志 |
| 状态保持 | 旋转屏幕不重建页面；进程被回收后回到上次的面板和页面 |
| 会话过期 | 页面跳到 `#/login`（面板对 401 的处理）时，用保存的密码静默重新登录一次；仍失败、需要两步验证或没保存密码时显示原生登录页；在网页里主动退出则回到面板列表 |
| 快速切换 | 屏幕左边缘中间的小把手打开抽屉：切换面板、刷新、在浏览器打开、面板列表、退出登录 |

## 和网页登录的关系

应用和网页登录页使用同一套接口（见 `internal/server/handlers_auth.go`、`handlers_2fa.go`）：

1. `POST /api/v1/auth/login`，`{"username","password"}`。成功时服务器下发 `atoll_session` Cookie（HttpOnly、SameSite=Strict、TLS 下 Secure、30 天）；账号开了两步验证则返回 `{"two_factor_required":true,"pending_token":…}`。
2. `POST /api/v1/auth/login/2fa`，`{"pending_token","code"}`，`code` 是 6 位 TOTP 或恢复码。
3. 原生请求不带 `Origin` 头，服务器的同源检查放行；拿到的 `Set-Cookie` 原样写进 WebView 的 CookieManager（同一个源），之后由 WebView 自己带 Cookie 访问面板和 WebSocket。

面板需要部署在域名根路径（网页本身用的就是 `/api/v1` 绝对路径）。自签名证书需要先把 CA 安装到系统「用户证书」，应用信任用户证书；局域网 `http://` 地址也可以用。

## 构建

本地需要 JDK 17 和 Android SDK（compileSdk 35）：

```bash
cd android
./gradlew assembleDebug        # app/build/outputs/apk/debug/
./gradlew assembleRelease      # 没有配置密钥时用 debug 密钥签名
```

发布签名通过环境变量传入：`ANDROID_KEYSTORE_FILE`、`ANDROID_KEYSTORE_PASSWORD`、`ANDROID_KEY_ALIAS`、`ANDROID_KEY_PASSWORD`；版本号用 `VERSION_NAME` / `VERSION_CODE`。

CI（`.github/workflows/android.yml`）在 `main` 上 `android/**` 有改动时、推送 `v*` 标签时和手动触发时运行：JDK 17 + 预装的 Android SDK 执行 `assembleRelease`。密钥来自仓库 Secrets `ANDROID_KEYSTORE_B64` / `ANDROID_KEYSTORE_PASSWORD` / `ANDROID_KEY_ALIAS` / `ANDROID_KEY_PASSWORD`；标签构建时 `versionName` 取标签（去掉 `v`），`versionCode` 取运行号，并等待 `release.yml` 创建 Release 后把 APK 和 `.sha256` 附上去；其他构建上传为 artifact。

## 代码结构

```
app/src/main/java/io/atoll/app/
  MainActivity.kt        路由（列表 / 表单 / 面板）、打开面板的流程、生物识别
  AtollApp.kt            Application：面板存储、本次进程内的临时密码
  data/PanelStore.kt     加密保存的面板列表
  net/AtollApi.kt        地址规范化、status / login / 2fa / me / logout、Cookie 写入、错误映射
  ui/                    主题（面板配色）、面板列表、添加 / 编辑 / 登录表单
  web/WebPanelScreen.kt  全屏 WebView、下拉刷新、抽屉、出错页、重新登录
  web/WebSupport.kt      注入脚本、JS 桥、下载、外部链接
```
