package io.atoll.app

import android.app.Application
import io.atoll.app.data.PanelStore

class AtollApp : Application() {
    val store: PanelStore by lazy { PanelStore(this) }

    /** Passwords the user typed but chose not to save: kept for this process only, for one silent re-login. */
    val memoryPasswords = mutableMapOf<String, String>()

    /** 2FA challenges started by a background login, picked up by the login screen. */
    val pendingChallenges = mutableMapOf<String, String>()
}
