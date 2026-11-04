package io.atoll.app.web

import android.app.DownloadManager
import android.content.ActivityNotFoundException
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.provider.MediaStore
import android.util.Base64
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.URLUtil
import android.webkit.WebViewClient
import android.widget.Toast
import androidx.browser.customtabs.CustomTabsIntent
import androidx.compose.ui.graphics.Color
import io.atoll.app.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.net.URLDecoder

/** scheme://host[:port] of a URI, lower-cased, default ports dropped (same shape as AtollApi.normalizeUrl). */
fun originOf(uri: Uri): String? {
    val scheme = uri.scheme?.lowercase() ?: return null
    val host = uri.host?.lowercase() ?: return null
    val port = uri.port
    val def = when (scheme) { "https" -> 443; "http" -> 80; else -> -1 }
    return if (port == -1 || port == def) "$scheme://$host" else "$scheme://$host:$port"
}

fun originOf(url: String?): String? = url?.let { runCatching { originOf(Uri.parse(it)) }.getOrNull() }

fun isLoginRoute(url: String?): Boolean {
    if (url == null) return false
    val frag = url.substringAfter('#', "")
    return frag == "/login" || frag.startsWith("/login?") || frag.startsWith("/login/")
}

fun toast(ctx: Context, text: String) = Toast.makeText(ctx, text, Toast.LENGTH_SHORT).show()

/** Other sites open outside the app: http(s) in a Custom Tab, everything else (mailto:, tel:…) via its app. */
fun openExternal(ctx: Context, uri: Uri) {
    val web = uri.scheme.equals("http", true) || uri.scheme.equals("https", true)
    try {
        if (web) {
            CustomTabsIntent.Builder().setShowTitle(true).build().launchUrl(ctx, uri)
        } else {
            ctx.startActivity(Intent(Intent.ACTION_VIEW, uri))
        }
    } catch (e: ActivityNotFoundException) {
        try {
            ctx.startActivity(Intent(Intent.ACTION_VIEW, uri))
        } catch (e2: Exception) {
            toast(ctx, ctx.getString(R.string.no_app_for_link))
        }
    } catch (e: Exception) {
        toast(ctx, ctx.getString(R.string.no_app_for_link))
    }
}

fun isOnline(ctx: Context): Boolean {
    val cm = ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return true
    val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return false
    return caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
}

fun webErrorText(ctx: Context, code: Int, description: CharSequence?): String {
    if (!isOnline(ctx)) return ctx.getString(R.string.err_offline)
    return when (code) {
        WebViewClient.ERROR_HOST_LOOKUP -> ctx.getString(R.string.err_dns)
        WebViewClient.ERROR_CONNECT, WebViewClient.ERROR_IO -> ctx.getString(R.string.err_unreachable)
        WebViewClient.ERROR_TIMEOUT -> ctx.getString(R.string.err_timeout)
        WebViewClient.ERROR_FAILED_SSL_HANDSHAKE -> ctx.getString(R.string.web_err_tls)
        else -> ctx.getString(R.string.web_err_generic, description?.toString() ?: code.toString())
    }
}

/** "#1f1e26", "rgb(31, 30, 38)" or "rgba(…)" → Color. */
fun parseCssColor(s: String): Color? {
    val v = s.trim()
    try {
        if (v.startsWith("#")) {
            val hex = v.substring(1)
            val full = when (hex.length) {
                3 -> hex.map { "$it$it" }.joinToString("")
                6 -> hex
                8 -> hex.substring(0, 6)
                else -> return null
            }
            return Color(0xFF000000 or full.toLong(16))
        }
        if (v.startsWith("rgb")) {
            val parts = v.substringAfter('(').substringBefore(')').split(',', ' ', '/').filter { it.isNotBlank() }
            if (parts.size < 3) return null
            val (r, g, b) = parts.take(3).map { it.trim().toFloat().toInt().coerceIn(0, 255) }
            if (parts.size >= 4 && parts[3].trim().toFloat() == 0f) return null // transparent
            return Color(r, g, b)
        }
    } catch (e: Exception) {
        return null
    }
    return null
}

/** Methods the page's injected shell script calls. Runs on a binder thread; hops to main. */
class AtollBridge(private val main: Handler, private val cb: Callbacks) {
    interface Callbacks {
        fun onTheme(bg: String, dark: Boolean)
        fun onCanRefresh(canRefresh: Boolean)
        fun onSaveDataUrl(name: String, dataUrl: String)
        fun onDownload(url: String, name: String)
    }

    @JavascriptInterface
    fun theme(bg: String?, dark: Boolean) {
        main.post { cb.onTheme(bg.orEmpty(), dark) }
    }

    @JavascriptInterface
    fun canRefresh(v: Boolean) = cb.onCanRefresh(v)

    @JavascriptInterface
    fun saveDataUrl(name: String?, dataUrl: String?) {
        if (dataUrl == null) return
        main.post { cb.onSaveDataUrl(name.orEmpty(), dataUrl) }
    }

    @JavascriptInterface
    fun download(url: String?, name: String?) {
        if (url == null) return
        main.post { cb.onDownload(url, name.orEmpty()) }
    }
}

object Downloads {
    private fun sanitize(name: String): String =
        name.replace(Regex("[\\\\/:*?\"<>|\\u0000-\\u001f]"), "_").trim().take(120).ifEmpty { "download" }

    /** A normal http(s) download, with the panel's cookie so authenticated API downloads work. */
    fun enqueue(ctx: Context, url: String, userAgent: String?, contentDisposition: String?, mime: String?, name: String?) {
        try {
            val fileName = name?.takeIf { it.isNotBlank() }?.let(::sanitize)
                ?: sanitize(URLUtil.guessFileName(url, contentDisposition, mime))
            val req = DownloadManager.Request(Uri.parse(url))
            CookieManager.getInstance().getCookie(url)?.let { req.addRequestHeader("Cookie", it) }
            if (!userAgent.isNullOrBlank()) req.addRequestHeader("User-Agent", userAgent)
            if (!mime.isNullOrBlank()) req.setMimeType(mime)
            req.setTitle(fileName)
            req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            if (Build.VERSION.SDK_INT >= 29) {
                req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, fileName)
            } else {
                req.setDestinationInExternalFilesDir(ctx, Environment.DIRECTORY_DOWNLOADS, fileName)
            }
            (ctx.getSystemService(Context.DOWNLOAD_SERVICE) as DownloadManager).enqueue(req)
            toast(ctx, ctx.getString(R.string.download_started, fileName))
        } catch (e: Exception) {
            toast(ctx, ctx.getString(R.string.download_failed, e.message ?: ""))
        }
    }

    /** Files the page builds itself (blob: / data: URLs, e.g. exported .env or recovery codes). */
    suspend fun saveDataUrl(ctx: Context, name: String, dataUrl: String): String = withContext(Dispatchers.IO) {
        val comma = dataUrl.indexOf(',')
        require(dataUrl.startsWith("data:") && comma > 0) { "bad data url" }
        val header = dataUrl.substring(5, comma)
        val payload = dataUrl.substring(comma + 1)
        val mime = header.substringBefore(';').ifEmpty { "application/octet-stream" }
        val bytes = if (header.endsWith(";base64")) Base64.decode(payload, Base64.DEFAULT)
        else URLDecoder.decode(payload, "UTF-8").toByteArray()
        val fileName = sanitize(name)
        if (Build.VERSION.SDK_INT >= 29) {
            val values = ContentValues().apply {
                put(MediaStore.MediaColumns.DISPLAY_NAME, fileName)
                put(MediaStore.MediaColumns.MIME_TYPE, mime)
                put(MediaStore.MediaColumns.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS)
            }
            val uri = ctx.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                ?: error("MediaStore insert failed")
            ctx.contentResolver.openOutputStream(uri)?.use { it.write(bytes) } ?: error("cannot write")
        } else {
            val dir = ctx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS) ?: ctx.filesDir
            File(dir, fileName).writeBytes(bytes)
        }
        fileName
    }
}

/**
 * Injected into the panel's pages (document start where supported). Reports the page
 * background for the system bars, tells the pull-to-refresh whether the content under the
 * finger is scrolled, and routes <a download> clicks (blob:/data:/same-origin) to native saving.
 */
const val SHELL_JS = """
(function () {
  if (window.__atollShell) return;
  window.__atollShell = 1;
  var B = window.AtollBridge;
  if (!B) return;
  function theme() {
    try {
      var de = document.documentElement;
      var bg = (getComputedStyle(de).getPropertyValue('--bg') || '').trim();
      if (!bg && document.body) bg = getComputedStyle(document.body).backgroundColor;
      B.theme(bg || '', !de.classList.contains('light'));
    } catch (e) {}
  }
  window.__atollTheme = theme;
  try { new MutationObserver(theme).observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'style'] }); } catch (e) {}
  document.addEventListener('DOMContentLoaded', theme);
  window.addEventListener('load', theme);
  function scrolled(el) {
    if ((window.scrollY || 0) > 0) return true;
    for (; el && el.nodeType === 1; el = el.parentElement) {
      if (el.scrollTop > 0) {
        var o = getComputedStyle(el).overflowY;
        if (o === 'auto' || o === 'scroll' || o === 'overlay') return true;
      }
    }
    return false;
  }
  document.addEventListener('touchstart', function (e) {
    try { B.canRefresh(!scrolled(e.target)); } catch (x) {}
  }, { capture: true, passive: true });
  var blobs = new Map();
  var createURL = URL.createObjectURL;
  URL.createObjectURL = function (o) {
    var u = createURL.apply(URL, arguments);
    try { if (o instanceof Blob) blobs.set(u, o); } catch (e) {}
    return u;
  };
  function saveBlob(name, blob) {
    var r = new FileReader();
    r.onload = function () { B.saveDataUrl(name || 'download', String(r.result)); };
    r.readAsDataURL(blob);
  }
  function handle(a) {
    var href = a.href || '';
    var name = a.getAttribute('download') || '';
    if (href.indexOf('blob:') === 0) {
      var b = blobs.get(href);
      if (b) { blobs.delete(href); saveBlob(name, b); return true; }
      fetch(href).then(function (r) { return r.blob(); }).then(function (b) { saveBlob(name, b); });
      return true;
    }
    if (href.indexOf('data:') === 0) { B.saveDataUrl(name || 'download', href); return true; }
    if (/^https?:/i.test(href)) { B.download(href, name); return true; }
    return false;
  }
  var click = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function () {
    if (this.hasAttribute('download') && handle(this)) return;
    return click.apply(this, arguments);
  };
  document.addEventListener('click', function (e) {
    var a = e.target && e.target.closest ? e.target.closest('a[download]') : null;
    if (a && handle(a)) { e.preventDefault(); e.stopPropagation(); }
  }, true);
})();
"""
