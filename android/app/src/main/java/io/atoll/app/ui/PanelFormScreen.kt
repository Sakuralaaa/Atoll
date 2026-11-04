package io.atoll.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import io.atoll.app.R
import io.atoll.app.data.Panel
import io.atoll.app.net.ApiError
import io.atoll.app.net.AtollApi
import io.atoll.app.net.LoginResult
import kotlinx.coroutines.launch

enum class FormMode { Add, Edit, Login }

/**
 * Add / edit a panel, or sign in to one. Signing in works like the web Login page:
 * password first, then (if the account has 2FA) a 6-digit code or a recovery code.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PanelFormScreen(
    mode: FormMode,
    panel: Panel?,
    initialError: String?,
    initialPendingToken: String?,
    canGoBack: Boolean,
    onBack: () -> Unit,
    /** Saved without signing in (edit, or "save only" when adding). */
    onSaved: (Panel) -> Unit,
    /** Signed in: the session cookie is already in the WebView's CookieManager. */
    onLoggedIn: (panel: Panel, password: String) -> Unit,
) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()

    var name by rememberSaveable { mutableStateOf(panel?.name.orEmpty()) }
    var url by rememberSaveable { mutableStateOf(panel?.url ?: "") }
    var username by rememberSaveable { mutableStateOf(panel?.username.orEmpty()) }
    // never put the password into saved instance state
    var password by remember { mutableStateOf("") }
    var rememberPw by rememberSaveable { mutableStateOf(panel?.remember ?: true) }
    var biometric by rememberSaveable { mutableStateOf(panel?.biometric ?: false) }
    var showPassword by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var error by rememberSaveable { mutableStateOf(initialError) }
    var pendingToken by rememberSaveable { mutableStateOf(initialPendingToken) }
    var pendingOrigin by rememberSaveable { mutableStateOf(panel?.url) }
    var code by rememberSaveable { mutableStateOf("") }
    var recovery by rememberSaveable { mutableStateOf(false) }

    val hasSavedPassword = panel?.password != null
    val title = when (mode) {
        FormMode.Add -> stringResource(R.string.add_panel)
        FormMode.Edit -> stringResource(R.string.edit_panel)
        FormMode.Login -> stringResource(R.string.login_to, panel?.name ?: "Atoll")
    }

    fun buildPanel(origin: String, pw: String?): Panel {
        val host = origin.substringAfter("://")
        val base = panel ?: Panel(name = "", url = origin, username = "")
        val keepPw = if (rememberPw) (pw?.takeIf { it.isNotEmpty() } ?: base.password) else null
        return base.copy(
            name = name.trim().ifEmpty { host },
            url = origin,
            username = username.trim(),
            remember = rememberPw,
            password = keepPw,
            biometric = biometric && rememberPw,
        )
    }

    fun finish(origin: String, pw: String, setCookie: String) {
        scope.launch {
            AtollApi.installSession(origin, setCookie)
            busy = false
            onLoggedIn(buildPanel(origin, pw), pw)
        }
    }

    fun submitPassword() {
        if (busy) return
        val origin = AtollApi.normalizeUrl(url)
        if (origin == null) { error = ctx.getString(R.string.err_bad_url); return }
        if (username.isBlank()) { error = ctx.getString(R.string.err_need_username); return }
        val pw = password.ifEmpty { if (mode == FormMode.Login || mode == FormMode.Edit) panel?.password.orEmpty() else "" }
        if (pw.isEmpty()) { error = ctx.getString(R.string.err_need_password); return }
        busy = true; error = null
        scope.launch {
            try {
                val info = AtollApi.checkPanel(origin)
                url = info.origin
                if (info.setupRequired) throw ApiError.SetupRequired()
                when (val r = AtollApi.login(info.origin, username, pw)) {
                    is LoginResult.Success -> finish(info.origin, pw, r.setCookie)
                    is LoginResult.TwoFactor -> {
                        password = pw
                        pendingToken = r.pendingToken
                        pendingOrigin = info.origin
                        code = ""
                        busy = false
                    }
                }
            } catch (e: ApiError) {
                error = e.text(ctx); busy = false
            } catch (e: Exception) {
                error = ctx.getString(R.string.err_unreachable) + " (" + e.javaClass.simpleName + ")"; busy = false
            }
        }
    }

    fun submitCode() {
        val token = pendingToken ?: return
        val origin = pendingOrigin ?: AtollApi.normalizeUrl(url) ?: return
        if (busy || code.isBlank()) return
        busy = true; error = null
        scope.launch {
            try {
                val r = AtollApi.login2fa(origin, token, code)
                val pw = password.ifEmpty { panel?.password.orEmpty() }
                finish(origin, pw, r.setCookie)
            } catch (e: ApiError) {
                if (e is ApiError.ChallengeExpired) pendingToken = null
                error = e.text(ctx); busy = false; code = ""
            } catch (e: Exception) {
                error = ctx.getString(R.string.err_unreachable); busy = false
            }
        }
    }

    fun saveOnly() {
        if (busy) return
        val origin = AtollApi.normalizeUrl(url)
        if (origin == null) { error = ctx.getString(R.string.err_bad_url); return }
        if (username.isBlank()) { error = ctx.getString(R.string.err_need_username); return }
        busy = true; error = null
        scope.launch {
            try {
                val info = AtollApi.checkPanel(origin)
                busy = false
                onSaved(buildPanel(info.origin, password))
            } catch (e: ApiError) {
                error = e.text(ctx); busy = false
            } catch (e: Exception) {
                error = ctx.getString(R.string.err_unreachable); busy = false
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title) },
                navigationIcon = {
                    if (canGoBack) IconButton(onClick = {
                        if (pendingToken != null) { pendingToken = null; error = null } else onBack()
                    }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back)) }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (pendingToken == null) {
                if (mode == FormMode.Add) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        AtollLogo(40)
                        Spacer(Modifier.size(12.dp))
                        Text(stringResource(R.string.add_panel_intro), color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                OutlinedTextField(
                    value = url, onValueChange = { url = it; error = null },
                    label = { Text(stringResource(R.string.panel_url)) },
                    placeholder = { Text("panel.example.com") },
                    supportingText = { Text(stringResource(R.string.panel_url_hint)) },
                    singleLine = true, enabled = !busy,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next),
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = name, onValueChange = { name = it },
                    label = { Text(stringResource(R.string.panel_name)) },
                    placeholder = { Text(stringResource(R.string.panel_name_hint)) },
                    singleLine = true, enabled = !busy,
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = username, onValueChange = { username = it; error = null },
                    label = { Text(stringResource(R.string.username)) },
                    singleLine = true, enabled = !busy,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email, imeAction = ImeAction.Next),
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = password, onValueChange = { password = it; error = null },
                    label = { Text(stringResource(R.string.password)) },
                    placeholder = { if (hasSavedPassword) Text(stringResource(R.string.password_keep_hint)) },
                    singleLine = true, enabled = !busy,
                    visualTransformation = if (showPassword) VisualTransformation.None else PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
                    keyboardActions = KeyboardActions(onDone = { if (mode == FormMode.Edit) saveOnly() else submitPassword() }),
                    trailingIcon = {
                        IconButton(onClick = { showPassword = !showPassword }) {
                            Icon(if (showPassword) Icons.Default.VisibilityOff else Icons.Default.Visibility, contentDescription = stringResource(R.string.show_password))
                        }
                    },
                    modifier = Modifier.fillMaxWidth(),
                )
                ToggleRow(stringResource(R.string.remember_password), stringResource(R.string.remember_password_hint), rememberPw, !busy) {
                    rememberPw = it
                    if (!it) biometric = false
                }
                ToggleRow(stringResource(R.string.biometric_unlock), stringResource(R.string.biometric_unlock_hint), biometric, !busy && rememberPw) { biometric = it }
            } else {
                Text(stringResource(R.string.two_factor_title), style = MaterialTheme.typography.titleMedium)
                Text(
                    stringResource(if (recovery) R.string.two_factor_recovery_hint else R.string.two_factor_hint),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                OutlinedTextField(
                    value = code,
                    onValueChange = { v ->
                        error = null
                        code = if (recovery) v.trim() else v.filter { it.isDigit() }.take(6)
                        if (!recovery && code.length == 6) submitCode()
                    },
                    label = { Text(stringResource(if (recovery) R.string.recovery_code else R.string.verification_code)) },
                    singleLine = true, enabled = !busy,
                    keyboardOptions = KeyboardOptions(
                        keyboardType = if (recovery) KeyboardType.Ascii else KeyboardType.NumberPassword,
                        imeAction = ImeAction.Done,
                    ),
                    keyboardActions = KeyboardActions(onDone = { submitCode() }),
                    modifier = Modifier.fillMaxWidth(),
                )
                TextButton(onClick = { recovery = !recovery; code = ""; error = null }) {
                    Text(stringResource(if (recovery) R.string.use_totp else R.string.use_recovery))
                }
            }

            error?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
            }

            Spacer(Modifier.height(4.dp))
            if (pendingToken != null) {
                Button(onClick = { submitCode() }, enabled = !busy && code.isNotBlank(), modifier = Modifier.fillMaxWidth().height(48.dp)) {
                    if (busy) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp) else Text(stringResource(R.string.verify))
                }
            } else if (mode == FormMode.Edit) {
                Button(onClick = { saveOnly() }, enabled = !busy, modifier = Modifier.fillMaxWidth().height(48.dp)) {
                    if (busy) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp) else Text(stringResource(R.string.save))
                }
                OutlinedButton(onClick = { submitPassword() }, enabled = !busy, modifier = Modifier.fillMaxWidth().height(48.dp)) {
                    Text(stringResource(R.string.save_and_login))
                }
            } else {
                Button(onClick = { submitPassword() }, enabled = !busy, modifier = Modifier.fillMaxWidth().height(48.dp)) {
                    if (busy) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp) else Text(stringResource(R.string.login))
                }
                if (mode == FormMode.Add) {
                    TextButton(onClick = { saveOnly() }, enabled = !busy, modifier = Modifier.fillMaxWidth()) {
                        Text(stringResource(R.string.save_only))
                    }
                }
            }
        }
    }
}

@Composable
private fun ToggleRow(title: String, hint: String, checked: Boolean, enabled: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 12.dp)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, color = if (enabled) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
            Text(hint, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(checked = checked, onCheckedChange = onChange, enabled = enabled)
    }
}
