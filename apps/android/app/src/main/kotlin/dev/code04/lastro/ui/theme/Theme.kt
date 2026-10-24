package dev.code04.lastro.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val LastroColors = darkColorScheme(
    primary = Color(0xFF4ADE80),
    onPrimary = Color(0xFF04210F),
    primaryContainer = Color(0xFF14532D),
    onPrimaryContainer = Color(0xFFBBF7D0),
    secondary = Color(0xFF8FD1A8),
    onSecondary = Color(0xFF0B2617),
    secondaryContainer = Color(0xFF1F3A2B),
    onSecondaryContainer = Color(0xFFCFEBD9),
    tertiary = Color(0xFFF2C14E),
    onTertiary = Color(0xFF2A1E00),
    tertiaryContainer = Color(0xFF3D2E05),
    onTertiaryContainer = Color(0xFFFCE7B0),
    error = Color(0xFFFF8A80),
    onError = Color(0xFF3B0906),
    errorContainer = Color(0xFF5C1A16),
    onErrorContainer = Color(0xFFFFDAD5),
    background = Color(0xFF0A100D),
    onBackground = Color(0xFFE2EAE5),
    surface = Color(0xFF0A100D),
    onSurface = Color(0xFFE2EAE5),
    surfaceVariant = Color(0xFF1C2721),
    onSurfaceVariant = Color(0xFFA3B5AA),
    surfaceContainerLowest = Color(0xFF070B09),
    surfaceContainerLow = Color(0xFF101814),
    surfaceContainer = Color(0xFF141E19),
    surfaceContainerHigh = Color(0xFF1A2620),
    surfaceContainerHighest = Color(0xFF213029),
    outline = Color(0xFF3A4D42),
    outlineVariant = Color(0xFF24312A),
)

@Composable
fun LastroTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = LastroColors, content = content)
}
