package io.atoll.app.net

import android.content.Context
import android.webkit.CookieManager
import io.atoll.app.BuildConfig
import io.atoll.app.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.IOException
import java.net.ConnectException
import java.net.HttpURLConnection
import java.net.NoRouteToHostException
import java.net.SocketTimeoutException
import java.net.URI
import java.net.URL
import java.net.UnknownHostException
import java.security.cert.CertificateException
import javax.net.ssl.SSLException
import kotlin.coroutines.resume

/** Everything that can go wrong talking to a panel, mapped to a user-facing message. */
sealed class ApiError(msg: String? = null) : Exception(msg) {
    class BadUrl : ApiError()
    class Dns(m: String?) : ApiError(m)
    class Unreachable(m: String?) : ApiError(m)
    class Timeout : ApiError()
    class Tls(m: String?) : ApiError(m)
    class NotAtoll(m: String?) : ApiError(m)
    class SetupRequired : ApiError()
    class InvalidCredentials : ApiError()
    class InvalidCode : ApiError()
    class ChallengeExpired : ApiError()
    class RateLimited(m: String?) : ApiError(m)
    class NoSession : ApiError()
    class Server(val status: Int, m: String?) : ApiError(m)

    /** Network-level failure: the panel could not be reached at all. */
    val isNetwork get() = this is Dns || this is Unreachable || this is Timeout || this is Tls

    fun text(ctx: Context): String = when (this) {
        is BadUrl -> ctx.getString(R.string.err_bad_url)
        is Dns -> ctx.getString(R.string.err_dns)
        is Unreachable -> ctx.getString(R.string.err_unreachable)
        is Timeout -> ctx.getString(R.string.err_timeout)
        is Tls -> ctx.getString(R.string.err_tls, message ?: "")
        is NotAtoll -> ctx.getString(R.string.err_not_atoll)
        is SetupRequired -> ctx.getString(R.string.err_setup_required)
        is InvalidCredentials -> ctx.getString(R.string.err_invalid_credentials)
        is InvalidCode -> ctx.getString(R.string.err_invalid_code)
        is ChallengeExpired -> ctx.getString(R.string.err_challenge_expired)
        is RateLimited -> ctx.getString(R.string.err_rate_limited, message ?: "")
        is NoSession -> ctx.getString(R.string.err_no_session)
        is Server -> ctx.getString(R.string.err_server, status, message ?: "")
    }
}

sealed class LoginResult {
    /** The raw Set-Cookie header carrying atoll_session. */
    data class Success(val setCookie: String) : LoginResult()
    data class TwoFactor(val pendingToken: String) : LoginResult()
}

/**
 * Speaks the same API as the web panel's Login page (web/src/api/client.ts):
 * POST /api/v1/auth/login {username,password} → session cookie `atoll_session`
 * (HttpOnly, SameSite=Strict, Secure on TLS, 30 days) or {two_factor_required, pending_token};
 * POST /api/v1/auth/login/2fa {pending_token, code}. Native requests send no Origin header,
 * which the server's same-origin check accepts.
 */
object AtollApi {
    const val SESSION_COOKIE = "atoll_session"
    private val userAgent = "AtollAndroid/${BuildConfig.VERSION_NAME}"

    /** "panel.example.com" → "https://panel.example.com"; keeps only scheme://host[:port]. */
    fun normalizeUrl(input: String): String? {
        var s = input.trim()
        if (s.isEmpty()) return null
        if (!s.contains("://")) s = "https://$s"
        return try {
            val u = URI(s)
            val scheme = u.scheme?.lowercase() ?: return null
            if (scheme != "http" && scheme != "https") return null
            val host = u.host?.lowercase() ?: return null
            if (host.isEmpty()) return null
            val port = u.port
            val defaultPort = if (scheme == "https") 443 else 80
            if (port == -1 || port == defaultPort) "$scheme://$host" else "$scheme://$host:$port"
        } catch (e: Exception) {
            null
        }
    }

    data class PanelInfo(val origin: String, val setupRequired: Boolean)

    /** GET /api/v1/auth/status; follows a redirect to https (or another host) up to 3 times. */
    suspend fun checkPanel(origin: String): PanelInfo = withContext(Dispatchers.IO) {
        var o = origin
        repeat(4) {
            val r = request("GET", "$o/api/v1/auth/status")
            if (r.code in 300..399 && r.location != null) {
                val next = normalizeUrl(URL(URL("$o/"), r.location).toString()) ?: throw ApiError.NotAtoll("redirect")
                if (next == o) throw ApiError.NotAtoll("redirect loop")
                o = next
                return@repeat
            }
            if (r.code != 200) throw ApiError.NotAtoll("HTTP ${r.code}")
            val j = r.json() ?: throw ApiError.NotAtoll("not json")
            if (!j.has("setup_required")) throw ApiError.NotAtoll("unexpected json")
            return@withContext PanelInfo(o, j.optBoolean("setup_required"))
        }
        throw ApiError.NotAtoll("too many redirects")
    }

    suspend fun login(origin: String, username: String, password: String): LoginResult = withContext(Dispatchers.IO) {
        val body = JSONObject().put("username", username.trim()).put("password", password)
        val r = request("POST", "$origin/api/v1/auth/login", body)
        when (r.code) {
            200 -> {
                val j = r.json()
                if (j != null && j.optBoolean("two_factor_required")) {
                    LoginResult.TwoFactor(j.optString("pending_token"))
                } else {
                    LoginResult.Success(r.sessionCookie() ?: throw ApiError.NoSession())
                }
            }
            else -> throw r.error()
        }
    }

    suspend fun login2fa(origin: String, pendingToken: String, code: String): LoginResult.Success = withContext(Dispatchers.IO) {
        val clean = code.trim().let { if (it.length == 7 && it[3] == ' ') it.replace(" ", "") else it }
        val body = JSONObject().put("pending_token", pendingToken).put("code", clean)
        val r = request("POST", "$origin/api/v1/auth/login/2fa", body)
        if (r.code == 200) LoginResult.Success(r.sessionCookie() ?: throw ApiError.NoSession()) else throw r.error()
    }

    /** True when the WebView already holds a session the server still accepts (GET /auth/me). */
    suspend fun hasValidSession(origin: String): Boolean {
        val cookie = withContext(Dispatchers.Main) { CookieManager.getInstance().getCookie(origin) } ?: return false
        if (!cookie.contains("$SESSION_COOKIE=")) return false
        return withContext(Dispatchers.IO) {
            val r = request("GET", "$origin/api/v1/auth/me", cookie = cookie)
            when (r.code) {
                200 -> true
                401, 403 -> false
                else -> throw ApiError.Server(r.code, r.errorMessage())
            }
        }
    }

    /** Hand the session to the WebView: same cookie, same attributes, persisted to disk. */
    suspend fun installSession(origin: String, setCookie: String) = withContext(Dispatchers.Main) {
        val cm = CookieManager.getInstance()
        cm.setAcceptCookie(true)
        suspendCancellableCoroutine { cont -> cm.setCookie(origin, setCookie) { cont.resume(Unit) } }
        cm.flush()
    }

    suspend fun logout(origin: String) {
        val cookie = withContext(Dispatchers.Main) { CookieManager.getInstance().getCookie(origin) }
        if (cookie != null) {
            try {
                withContext(Dispatchers.IO) { request("POST", "$origin/api/v1/auth/logout", JSONObject(), cookie) }
            } catch (_: Exception) {
            }
        }
        clearSession(origin)
    }

    suspend fun clearSession(origin: String) = withContext(Dispatchers.Main) {
        val cm = CookieManager.getInstance()
        suspendCancellableCoroutine { cont -> cm.setCookie(origin, "$SESSION_COOKIE=; Path=/; Max-Age=0") { cont.resume(Unit) } }
        cm.flush()
    }

    private class Resp(val code: Int, val body: String, val setCookies: List<String>, val location: String?) {
        fun json(): JSONObject? = try {
            JSONObject(body)
        } catch (e: Exception) {
            null
        }

        fun errorMessage(): String? = json()?.optString("error")?.takeIf { it.isNotEmpty() }

        fun sessionCookie(): String? = setCookies.firstOrNull { it.trimStart().startsWith("$SESSION_COOKIE=") }

        fun error(): ApiError {
            val j = json()
            val code = j?.optString("code").orEmpty()
            val msg = j?.optString("error")?.takeIf { it.isNotEmpty() }
            return when {
                code == "invalid_credentials" -> ApiError.InvalidCredentials()
                code == "invalid_code" -> ApiError.InvalidCode()
                code == "challenge_expired" -> ApiError.ChallengeExpired()
                this.code == 429 -> ApiError.RateLimited(msg)
                j == null && this.code == 404 -> ApiError.NotAtoll("HTTP 404")
                else -> ApiError.Server(this.code, msg)
            }
        }
    }

    private fun request(method: String, url: String, body: JSONObject? = null, cookie: String? = null): Resp {
        val conn = try {
            URL(url).openConnection() as HttpURLConnection
        } catch (e: Exception) {
            throw ApiError.BadUrl()
        }
        try {
            conn.requestMethod = method
            conn.instanceFollowRedirects = false
            conn.connectTimeout = 10_000
            conn.readTimeout = 20_000
            conn.useCaches = false
            conn.setRequestProperty("Accept", "application/json")
            conn.setRequestProperty("User-Agent", userAgent)
            if (cookie != null) conn.setRequestProperty("Cookie", cookie)
            if (body != null) {
                conn.doOutput = true
                conn.setRequestProperty("Content-Type", "application/json")
                conn.outputStream.use { it.write(body.toString().toByteArray()) }
            }
            val code = conn.responseCode
            val stream = if (code >= 400) conn.errorStream else conn.inputStream
            val text = stream?.use { s -> s.readBytes().take(1 shl 20).toByteArray().toString(Charsets.UTF_8) } ?: ""
            val cookies = conn.headerFields.entries
                .filter { it.key != null && it.key.equals("Set-Cookie", ignoreCase = true) }
                .flatMap { it.value }
            return Resp(code, text, cookies, conn.getHeaderField("Location"))
        } catch (e: ApiError) {
            throw e
        } catch (e: UnknownHostException) {
            throw ApiError.Dns(e.message)
        } catch (e: SocketTimeoutException) {
            throw ApiError.Timeout()
        } catch (e: SSLException) {
            throw ApiError.Tls(e.message)
        } catch (e: ConnectException) {
            throw ApiError.Unreachable(e.message)
        } catch (e: NoRouteToHostException) {
            throw ApiError.Unreachable(e.message)
        } catch (e: IOException) {
            if (e.cause is CertificateException) throw ApiError.Tls(e.message)
            throw ApiError.Unreachable(e.message)
        } finally {
            conn.disconnect()
        }
    }
}
