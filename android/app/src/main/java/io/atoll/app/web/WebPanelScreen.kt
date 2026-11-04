package io.atoll.app.web

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.graphics.Bitmap
import android.net.Uri
import android.net.http.SslError
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.RenderProcessGoneDetail
import android.webkit.SslErrorHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.OpenInBrowser
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.NavigationDrawerItem
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.rememberDrawerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.view.WindowCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import io.atoll.app.BuildConfig
import io.atoll.app.R
import io.atoll.app.data.Panel
import io.atoll.app.net.ApiError
import io.atoll.app.net.AtollApi
import io.atoll.app.net.LoginResult
import io.atoll.app.ui.AtollColors
import io.atoll.app.ui.AtollLogo
import kotlinx.coroutines.launch

private class ShellFlags {
    @Volatile var canRefresh = true
    @Volatile var logoutRequested = false
    var reloginAt = 0L
    var clearHistory = false
    var handlingLogin = false
}

@SuppressLint("SetJavaScriptEnabled")
@Composable
fun WebPanelScreen(
    panel: Panel,
    panels: List<Panel>,
    savedUrl: String?,
    passwordFor: (Panel) -> String?,
    onUrlChange: (String) -> Unit,
    onExit: () -> Unit,
    onSwitch: (Panel) -> Unit,
    /** The session is gone and could not be renewed silently: show the native login. */
    onNeedLogin: (error: String?, pendingToken: String?) -> Unit,
    /** The user logged out (in the page or from the drawer). */
    onLoggedOut: () -> Unit,
) {
    val ctx = LocalContext.current
    val activity = ctx as Activity
    val view = LocalView.current
    val scope = rememberCoroutineScope()
    val systemDark = isSystemInDarkTheme()
    val origin = panel.url

    var generation by remember { mutableIntStateOf(0) }
    var progress by remember { mutableIntStateOf(0) }
    var loading by remember { mutableStateOf(true) }
    var errorText by remember { mutableStateOf<String?>(null) }
    var failedUrl by remember { mutableStateOf<String?>(null) }
    var pageBg by remember { mutableStateOf(if (systemDark) AtollColors.DarkBg else AtollColors.LightBg) }
    var pageDark by remember { mutableStateOf(systemDark) }
    var relogging by remember { mutableStateOf(false) }
    val drawerState = rememberDrawerState(DrawerValue.Closed)
    val flags = remember { ShellFlags() }
    val chooser = remember { mutableStateOf<ValueCallback<Array<Uri>?>?>(null) }

    val needLogin by rememberUpdatedState(onNeedLogin)
    val loggedOut by rememberUpdatedState(onLoggedOut)
    val urlChange by rememberUpdatedState(onUrlChange)
    val pwFor by rememberUpdatedState(passwordFor)
    val currentSystemDark by rememberUpdatedState(systemDark)

    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { res ->
        val cb = chooser.value
        chooser.value = null
        val data = res.data
        val uris: Array<Uri>? = if (res.resultCode != Activity.RESULT_OK || data == null) null else {
            val clip = data.clipData
            when {
                clip != null && clip.itemCount > 0 -> Array(clip.itemCount) { clip.getItemAt(it).uri }
                data.data != null -> arrayOf(data.data!!)
                else -> null
            }
        }
        cb?.onReceiveValue(uris)
    }

    val holder = remember(generation) {
        val wv = WebView(ctx)
        val srl = SwipeRefreshLayout(ctx)
        srl.addView(wv, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        srl.setColorSchemeColors(AtollColors.Brand.toArgb())
        srl.setOnRefreshListener { errorText = null; wv.reload() }
        srl.setOnChildScrollUpCallback { _, _ -> wv.scrollY > 0 || !flags.canRefresh }

        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG)
        wv.setBackgroundColor(pageBg.toArgb())
        with(wv.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            setSupportMultipleWindows(false)
            javaScriptCanOpenWindowsAutomatically = false
            allowFileAccess = false
            allowContentAccess = false
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            builtInZoomControls = false
            userAgentString = "$userAgentString AtollAndroid/${BuildConfig.VERSION_NAME}"
        }
        CookieManager.getInstance().setAcceptCookie(true)
        CookieManager.getInstance().setAcceptThirdPartyCookies(wv, false)

        val main = Handler(Looper.getMainLooper())
        wv.addJavascriptInterface(AtollBridge(main, object : AtollBridge.Callbacks {
            override fun onTheme(bg: String, dark: Boolean) {
                if (originOf(wv.url) != origin) return
                pageDark = dark
                parseCssColor(bg)?.let { pageBg = it; wv.setBackgroundColor(it.toArgb()) }
            }

            override fun onCanRefresh(canRefresh: Boolean) {
                flags.canRefresh = canRefresh
            }

            override fun onSaveDataUrl(name: String, dataUrl: String) {
                if (originOf(wv.url) != origin) return
                scope.launch {
                    try {
                        val saved = Downloads.saveDataUrl(ctx, name, dataUrl)
                        toast(ctx, ctx.getString(R.string.saved_to_downloads, saved))
                    } catch (e: Exception) {
                        toast(ctx, ctx.getString(R.string.download_failed, e.message ?: ""))
                    }
                }
            }

            override fun onDownload(url: String, name: String) {
                if (originOf(wv.url) != origin) return
                Downloads.enqueue(ctx, url, wv.settings.userAgentString, null, null, name)
            }
        }), "AtollBridge")

        val docStart = WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT) && try {
            WebViewCompat.addDocumentStartJavaScript(wv, SHELL_JS, setOf(origin)); true
        } catch (e: Exception) {
            false
        }

        fun handleLoginRoute(v: WebView) {
            if (flags.handlingLogin) return
            flags.handlingLogin = true
            if (flags.logoutRequested) {
                flags.logoutRequested = false
                scope.launch { AtollApi.clearSession(origin); loggedOut() }
                return
            }
            val pw = pwFor(panel)
            val now = SystemClock.elapsedRealtime()
            if (pw.isNullOrEmpty() || (flags.reloginAt != 0L && now - flags.reloginAt < 30_000)) {
                needLogin(null, null)
                return
            }
            flags.reloginAt = now
            relogging = true
            scope.launch {
                try {
                    when (val r = AtollApi.login(origin, panel.username, pw)) {
                        is LoginResult.Success -> {
                            AtollApi.installSession(origin, r.setCookie)
                            flags.clearHistory = true
                            flags.handlingLogin = false
                            relogging = false
                            // full reload so the page starts over with the new session
                            v.evaluateJavascript("location.hash = '#/'; location.reload();", null)
                        }
                        is LoginResult.TwoFactor -> {
                            relogging = false
                            needLogin(null, r.pendingToken)
                        }
                    }
                } catch (e: ApiError) {
                    relogging = false
                    if (e.isNetwork) {
                        flags.handlingLogin = false
                        failedUrl = "$origin/"
                        errorText = e.text(ctx)
                    } else {
                        needLogin(e.text(ctx), null)
                    }
                } catch (e: Exception) {
                    relogging = false
                    needLogin(null, null)
                }
            }
        }

        fun onUrl(v: WebView, url: String?) {
            if (url == null || originOf(url) != origin) return
            if (isLoginRoute(url)) {
                handleLoginRoute(v)
            } else {
                flags.handlingLogin = false
                urlChange(url)
            }
        }

        wv.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val uri = request.url
                return when (uri.scheme?.lowercase()) {
                    "http", "https" -> if (originOf(uri) == origin) false else { openExternal(ctx, uri); true }
                    "about", "blob", "data", "javascript" -> false
                    else -> { openExternal(ctx, uri); true }
                }
            }

            override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
                loading = true
            }

            override fun onPageFinished(view: WebView, url: String?) {
                loading = false
                srl.isRefreshing = false
                if (originOf(url) == origin) {
                    if (!docStart) view.evaluateJavascript(SHELL_JS, null)
                    view.evaluateJavascript("window.__atollTheme && window.__atollTheme()", null)
                }
                if (flags.clearHistory) {
                    flags.clearHistory = false
                    view.clearHistory()
                }
                onUrl(view, url)
            }

            override fun doUpdateVisitedHistory(view: WebView, url: String?, isReload: Boolean) {
                onUrl(view, url)
            }

            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
                // remember a logout the user asked for in the page, so #/login then means "logged out", not "expired"
                if (request.method.equals("POST", true) && request.url.path == "/api/v1/auth/logout") {
                    flags.logoutRequested = true
                }
                return null
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame) {
                    failedUrl = request.url.toString()
                    errorText = webErrorText(ctx, error.errorCode, error.description)
                    srl.isRefreshing = false
                }
            }

            override fun onReceivedHttpError(view: WebView, request: WebResourceRequest, response: WebResourceResponse) {
                if (request.isForMainFrame && response.statusCode >= 500) {
                    failedUrl = request.url.toString()
                    errorText = ctx.getString(R.string.web_err_http, response.statusCode)
                    srl.isRefreshing = false
                }
            }

            @SuppressLint("WebViewClientOnReceivedSslError")
            override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
                handler.cancel()
                if (originOf(error.url) == origin) {
                    failedUrl = error.url
                    errorText = ctx.getString(R.string.web_err_tls)
                }
            }

            override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
                // the renderer crashed or was killed for memory: start over with a fresh WebView
                generation++
                return true
            }
        }

        wv.webChromeClient = object : WebChromeClient() {
            override fun onProgressChanged(view: WebView, newProgress: Int) {
                progress = newProgress
            }

            override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>?>, params: WebChromeClient.FileChooserParams): Boolean {
                chooser.value?.onReceiveValue(null)
                chooser.value = callback
                return try {
                    val intent = params.createIntent()
                    if (params.mode == WebChromeClient.FileChooserParams.MODE_OPEN_MULTIPLE) intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
                    launcher.launch(Intent.createChooser(intent, null))
                    true
                } catch (e: Exception) {
                    chooser.value = null
                    callback.onReceiveValue(null)
                    true
                }
            }
        }

        wv.setDownloadListener { url, userAgent, contentDisposition, mimetype, _ ->
            Downloads.enqueue(ctx, url, userAgent, contentDisposition, mimetype, null)
        }

        val start = savedUrl?.takeIf { originOf(it) == origin && !isLoginRoute(it) } ?: "$origin/"
        wv.loadUrl(start)
        srl to wv
    }
    val (swipe, webView) = holder

    // pause JS timers etc. while the app is in the background
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner, webView) {
        val obs = LifecycleEventObserver { _, e ->
            when (e) {
                Lifecycle.Event.ON_PAUSE -> { webView.onPause(); CookieManager.getInstance().flush() }
                Lifecycle.Event.ON_RESUME -> webView.onResume()
                else -> {}
            }
        }
        lifecycleOwner.lifecycle.addObserver(obs)
        onDispose { lifecycleOwner.lifecycle.removeObserver(obs) }
    }
    DisposableEffect(webView) {
        onDispose {
            chooser.value?.onReceiveValue(null)
            chooser.value = null
            webView.stopLoading()
            swipe.removeAllViews()
            webView.destroy()
        }
    }

    // system bar icons follow the page (dark panel → light icons)
    SideEffect {
        val c = WindowCompat.getInsetsController(activity.window, view)
        val light = !pageDark && pageBg.luminance() > 0.5f
        c.isAppearanceLightStatusBars = light
        c.isAppearanceLightNavigationBars = light
    }
    DisposableEffect(Unit) {
        onDispose {
            val c = WindowCompat.getInsetsController(activity.window, view)
            c.isAppearanceLightStatusBars = !currentSystemDark
            c.isAppearanceLightNavigationBars = !currentSystemDark
        }
    }

    fun canGoBackInPage(): Boolean {
        val list = webView.copyBackForwardList()
        val i = list.currentIndex
        if (i <= 0) return false
        return !isLoginRoute(list.getItemAtIndex(i - 1)?.url)
    }

    BackHandler {
        when {
            drawerState.isOpen -> scope.launch { drawerState.close() }
            errorText == null && canGoBackInPage() -> webView.goBack()
            else -> onExit()
        }
    }

    val onPage = if (pageDark) Color(0xFFF1F0EC) else Color(0xFF141414)

    Box(Modifier.fillMaxSize().background(pageBg)) {
        ModalNavigationDrawer(
            drawerState = drawerState,
            gesturesEnabled = drawerState.isOpen,
            drawerContent = {
                ModalDrawerSheet {
                    Column(Modifier.verticalScroll(rememberScrollState()).padding(vertical = 12.dp)) {
                        Row(Modifier.padding(horizontal = 24.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                            AtollLogo(32)
                            Spacer(Modifier.width(12.dp))
                            Column {
                                Text(panel.name, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(origin.substringAfter("://"), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                        HorizontalDivider(Modifier.padding(vertical = 8.dp))
                        Text(stringResource(R.string.switch_panel), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 28.dp, vertical = 8.dp))
                        panels.sortedByDescending { it.lastUsed }.forEach { p ->
                            NavigationDrawerItem(
                                label = { Text(p.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                                icon = { Icon(Icons.Default.Dns, contentDescription = null) },
                                selected = p.id == panel.id,
                                onClick = {
                                    scope.launch { drawerState.close() }
                                    if (p.id != panel.id) onSwitch(p)
                                },
                                modifier = Modifier.padding(horizontal = 12.dp),
                            )
                        }
                        HorizontalDivider(Modifier.padding(vertical = 8.dp))
                        NavigationDrawerItem(
                            label = { Text(stringResource(R.string.refresh)) },
                            icon = { Icon(Icons.Default.Refresh, contentDescription = null) },
                            selected = false,
                            onClick = { scope.launch { drawerState.close() }; errorText = null; webView.reload() },
                            modifier = Modifier.padding(horizontal = 12.dp),
                        )
                        NavigationDrawerItem(
                            label = { Text(stringResource(R.string.open_in_browser)) },
                            icon = { Icon(Icons.Default.OpenInBrowser, contentDescription = null) },
                            selected = false,
                            onClick = { scope.launch { drawerState.close() }; openExternal(ctx, Uri.parse(webView.url ?: "$origin/")) },
                            modifier = Modifier.padding(horizontal = 12.dp),
                        )
                        NavigationDrawerItem(
                            label = { Text(stringResource(R.string.all_panels)) },
                            icon = { Icon(Icons.AutoMirrored.Filled.List, contentDescription = null) },
                            selected = false,
                            onClick = { scope.launch { drawerState.close() }; onExit() },
                            modifier = Modifier.padding(horizontal = 12.dp),
                        )
                        NavigationDrawerItem(
                            label = { Text(stringResource(R.string.logout)) },
                            icon = { Icon(Icons.AutoMirrored.Filled.Logout, contentDescription = null) },
                            selected = false,
                            onClick = {
                                scope.launch {
                                    drawerState.close()
                                    AtollApi.logout(origin)
                                    loggedOut()
                                }
                            },
                            modifier = Modifier.padding(horizontal = 12.dp),
                        )
                    }
                }
            },
        ) {
            Box(Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing)) {
                key(generation) {
                    AndroidView(factory = { swipe }, modifier = Modifier.fillMaxSize())
                }
                if (loading && progress in 1..99) {
                    LinearProgressIndicator(
                        progress = { progress / 100f },
                        color = AtollColors.Brand,
                        trackColor = Color.Transparent,
                        modifier = Modifier.fillMaxWidth().height(3.dp).align(Alignment.TopCenter),
                    )
                }
                // small handle on the left edge: opens the panel switcher
                Box(
                    Modifier
                        .align(Alignment.CenterStart)
                        .size(width = 22.dp, height = 72.dp)
                        .clickable { scope.launch { drawerState.open() } },
                    contentAlignment = Alignment.CenterStart,
                ) {
                    Box(
                        Modifier
                            .size(width = 6.dp, height = 48.dp)
                            .clip(RoundedCornerShape(topEnd = 4.dp, bottomEnd = 4.dp))
                            .background(AtollColors.Brand.copy(alpha = 0.6f)),
                    )
                }
                errorText?.let { msg ->
                    Column(
                        Modifier.fillMaxSize().background(pageBg).padding(32.dp),
                        verticalArrangement = Arrangement.Center,
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        Icon(Icons.Default.CloudOff, contentDescription = null, tint = onPage, modifier = Modifier.size(48.dp))
                        Spacer(Modifier.height(16.dp))
                        Text(stringResource(R.string.web_error_title), color = onPage, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                        Spacer(Modifier.height(8.dp))
                        Text(msg, color = onPage.copy(alpha = 0.75f), textAlign = TextAlign.Center)
                        Spacer(Modifier.height(24.dp))
                        Button(onClick = {
                            errorText = null
                            webView.loadUrl(failedUrl ?: "$origin/")
                        }) { Text(stringResource(R.string.retry)) }
                        Spacer(Modifier.height(8.dp))
                        OutlinedButton(onClick = onExit) { Text(stringResource(R.string.all_panels), color = onPage) }
                    }
                }
                if (relogging) {
                    Column(
                        Modifier.fillMaxSize().background(pageBg),
                        verticalArrangement = Arrangement.Center,
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        CircularProgressIndicator(color = AtollColors.Brand)
                        Spacer(Modifier.height(16.dp))
                        Text(stringResource(R.string.relogging), color = onPage)
                    }
                }
            }
        }
    }
}
