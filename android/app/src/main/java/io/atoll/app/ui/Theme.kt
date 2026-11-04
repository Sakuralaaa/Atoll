package io.atoll.app.ui

import android.app.Activity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat

/** The panel's own palette (web/src/index.css). */
object AtollColors {
    val DarkBg = Color(0xFF1F1E26)
    val DarkCard = Color(0xFF2C2B35)
    val DarkCard2 = Color(0xFF35343F)
    val DarkLine = Color(0xFF3B3A45)
    val DarkFg = Color(0xFFF1F0EC)
    val DarkMuted = Color(0xFF9A9AA4)
    val LightBg = Color(0xFFF3EFE6)
    val LightCard = Color(0xFFFFFDF8)
    val LightCard2 = Color(0xFFF6F2E9)
    val LightLine = Color(0xFFD9D2C3)
    val LightFg = Color(0xFF141414)
    val LightMuted = Color(0xFF5C5A55)
    val Brand = Color(0xFF8B5CF6)
    val BrandLight = Color(0xFFA78BFA)
    val Accent = Color(0xFFB69CFF)
    val AccentLight = Color(0xFF6D28D9)
    val Danger = Color(0xFFF87171)
}

private val Dark = darkColorScheme(
    primary = AtollColors.Brand,
    onPrimary = Color.White,
    primaryContainer = AtollColors.DarkCard2,
    onPrimaryContainer = AtollColors.DarkFg,
    secondary = AtollColors.Accent,
    background = AtollColors.DarkBg,
    onBackground = AtollColors.DarkFg,
    surface = AtollColors.DarkBg,
    onSurface = AtollColors.DarkFg,
    surfaceVariant = AtollColors.DarkCard,
    onSurfaceVariant = AtollColors.DarkMuted,
    surfaceContainer = AtollColors.DarkCard,
    surfaceContainerLow = AtollColors.DarkCard,
    surfaceContainerHigh = AtollColors.DarkCard2,
    surfaceContainerHighest = AtollColors.DarkCard2,
    outline = AtollColors.DarkLine,
    outlineVariant = AtollColors.DarkLine,
    error = AtollColors.Danger,
)

private val Light = lightColorScheme(
    primary = AtollColors.AccentLight,
    onPrimary = Color.White,
    primaryContainer = AtollColors.BrandLight,
    onPrimaryContainer = AtollColors.LightFg,
    secondary = AtollColors.AccentLight,
    background = AtollColors.LightBg,
    onBackground = AtollColors.LightFg,
    surface = AtollColors.LightBg,
    onSurface = AtollColors.LightFg,
    surfaceVariant = AtollColors.LightCard,
    onSurfaceVariant = AtollColors.LightMuted,
    surfaceContainer = AtollColors.LightCard,
    surfaceContainerLow = AtollColors.LightCard,
    surfaceContainerHigh = AtollColors.LightCard2,
    surfaceContainerHighest = AtollColors.LightCard2,
    outline = AtollColors.LightLine,
    outlineVariant = AtollColors.LightLine,
)

@Composable
fun AtollTheme(content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            val window = (view.context as Activity).window
            val c = WindowCompat.getInsetsController(window, view)
            c.isAppearanceLightStatusBars = !dark
            c.isAppearanceLightNavigationBars = !dark
        }
    }
    MaterialTheme(colorScheme = if (dark) Dark else Light, content = content)
}
