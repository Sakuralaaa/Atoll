package io.atoll.app

import android.os.Bundle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.fragment.app.FragmentActivity
import io.atoll.app.data.Panel
import io.atoll.app.net.ApiError
import io.atoll.app.net.AtollApi
import io.atoll.app.net.LoginResult
import io.atoll.app.ui.AtollTheme
import io.atoll.app.ui.FormMode
import io.atoll.app.ui.PanelFormScreen
import io.atoll.app.ui.PanelListScreen
import io.atoll.app.web.WebPanelScreen
import io.atoll.app.web.toast
import kotlinx.coroutines.launch

class MainActivity : FragmentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        installSplashScreen()
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val app = application as AtollApp
        setContent {
            AtollTheme {
                Surface(color = MaterialTheme.colorScheme.background) {
                    AtollRoot(this, app)
                }
            }
        }
    }

    /** Fingerprint / face / screen lock before a panel with a saved password is opened. */
    fun authenticate(panel: Panel, onSuccess: () -> Unit) {
        val authenticators = BiometricManager.Authenticators.BIOMETRIC_WEAK or BiometricManager.Authenticators.DEVICE_CREDENTIAL
        if (BiometricManager.from(this).canAuthenticate(authenticators) != BiometricManager.BIOMETRIC_SUCCESS) {
            // no screen lock is set up on this device: there is nothing to verify against
            toast(this, getString(R.string.biometric_unavailable))
            onSuccess()
            return
        }
        val prompt = BiometricPrompt(this, ContextCompat.getMainExecutor(this), object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) = onSuccess()
            override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                if (errorCode != BiometricPrompt.ERROR_USER_CANCELED && errorCode != BiometricPrompt.ERROR_CANCELED &&
                    errorCode != BiometricPrompt.ERROR_NEGATIVE_BUTTON
                ) {
                    toast(this@MainActivity, errString.toString())
                }
            }
        })
        prompt.authenticate(
            BiometricPrompt.PromptInfo.Builder()
                .setTitle(getString(R.string.biometric_title))
                .setSubtitle(panel.name)
                .setAllowedAuthenticators(authenticators)
                .build(),
        )
    }
}

/** Screen state, kept as a string so it survives rotation and process death. */
private data class Route(val kind: String, val panelId: String? = null, val mode: String? = null) {
    fun encode() = "$kind|${panelId.orEmpty()}|${mode.orEmpty()}"

    companion object {
        val Home = Route("list")
        fun parse(s: String): Route {
            val p = s.split('|')
            return Route(p.getOrElse(0) { "list" }, p.getOrNull(1)?.ifEmpty { null }, p.getOrNull(2)?.ifEmpty { null })
        }
    }
}

@Composable
private fun AtollRoot(activity: MainActivity, app: AtollApp) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val panels by app.store.panels.collectAsState()
    var routeStr by rememberSaveable {
        mutableStateOf(if (app.store.panels.value.isEmpty()) Route("form", null, FormMode.Add.name).encode() else Route.Home.encode())
    }
    var formError by rememberSaveable { mutableStateOf<String?>(null) }
    var webUrl by rememberSaveable { mutableStateOf<String?>(null) }
    var opening by remember { mutableStateOf<String?>(null) }
    val route = Route.parse(routeStr)

    fun go(r: Route) {
        routeStr = r.encode()
    }

    fun toLogin(p: Panel, error: String?) {
        formError = error
        go(Route("form", p.id, FormMode.Login.name))
    }

    fun toWeb(p: Panel) {
        app.store.touch(p.id)
        webUrl = null
        go(Route("web", p.id))
    }

    fun connect(p: Panel) {
        opening = p.name
        scope.launch {
            try {
                val valid = try {
                    AtollApi.hasValidSession(p.url)
                } catch (e: ApiError) {
                    // unreachable or 5xx: open the page anyway, it shows the error with a Retry button
                    e.isNetwork || e is ApiError.Server
                }
                if (valid) {
                    toWeb(p)
                    return@launch
                }
                val pw = p.password ?: app.memoryPasswords[p.id]
                if (pw.isNullOrEmpty()) {
                    toLogin(p, null)
                    return@launch
                }
                when (val r = AtollApi.login(p.url, p.username, pw)) {
                    is LoginResult.Success -> {
                        AtollApi.installSession(p.url, r.setCookie)
                        toWeb(p)
                    }
                    is LoginResult.TwoFactor -> {
                        app.pendingChallenges[p.id] = r.pendingToken
                        toLogin(p, null)
                    }
                }
            } catch (e: ApiError) {
                if (e.isNetwork || e is ApiError.RateLimited) toast(ctx, e.text(ctx)) else toLogin(p, e.text(ctx))
            } catch (e: Exception) {
                toast(ctx, ctx.getString(R.string.err_unreachable))
            } finally {
                opening = null
            }
        }
    }

    fun openPanel(p: Panel) {
        if (opening != null) return
        if (p.biometric && p.password != null) activity.authenticate(p) { connect(p) } else connect(p)
    }

    when (route.kind) {
        "form" -> {
            val mode = runCatching { FormMode.valueOf(route.mode ?: "") }.getOrDefault(FormMode.Add)
            val panel = app.store.get(route.panelId)
            val canGoBack = panels.isNotEmpty()
            BackHandler(enabled = canGoBack) { formError = null; go(Route.Home) }
            key(routeStr) {
                val pending = remember { panel?.let { app.pendingChallenges.remove(it.id) } }
                PanelFormScreen(
                    mode = mode,
                    panel = panel,
                    initialError = formError,
                    initialPendingToken = pending,
                    canGoBack = canGoBack,
                    onBack = { formError = null; go(Route.Home) },
                    onSaved = { np ->
                        app.store.upsert(np)
                        formError = null
                        go(Route.Home)
                    },
                    onLoggedIn = { np, pw ->
                        app.store.upsert(np)
                        if (pw.isNotEmpty()) app.memoryPasswords[np.id] = pw
                        formError = null
                        toWeb(np)
                    },
                )
            }
        }
        "web" -> {
            val panel = app.store.get(route.panelId)
            if (panel == null) {
                go(Route.Home)
            } else {
                key(panel.id) {
                    WebPanelScreen(
                        panel = panel,
                        panels = panels,
                        savedUrl = webUrl,
                        passwordFor = { it.password ?: app.memoryPasswords[it.id] },
                        onUrlChange = { webUrl = it },
                        onExit = { go(Route.Home) },
                        onSwitch = { openPanel(it) },
                        onNeedLogin = { err, token ->
                            if (token != null) app.pendingChallenges[panel.id] = token
                            toLogin(panel, err)
                        },
                        onLoggedOut = { go(Route.Home) },
                    )
                }
            }
        }
        else -> PanelListScreen(
            panels = panels,
            onOpen = { openPanel(it) },
            onAdd = { formError = null; go(Route("form", null, FormMode.Add.name)) },
            onEdit = { formError = null; go(Route("form", it.id, FormMode.Edit.name)) },
            onDelete = { p ->
                app.store.delete(p.id)
                app.memoryPasswords.remove(p.id)
                if (panels.none { it.id != p.id && it.url == p.url }) scope.launch { AtollApi.clearSession(p.url) }
            },
        )
    }

    opening?.let { name ->
        Box(
            Modifier
                .fillMaxSize()
                .background(Color.Black.copy(alpha = 0.55f))
                .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) {},
            contentAlignment = Alignment.Center,
        ) {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                CircularProgressIndicator()
                Spacer(Modifier.height(16.dp))
                Text(stringResource(R.string.connecting, name), color = Color.White)
            }
        }
    }
}
