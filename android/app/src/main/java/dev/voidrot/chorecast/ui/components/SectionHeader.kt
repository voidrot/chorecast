package dev.voidrot.chorecast.ui.components

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics

/** A section title that screen readers can jump to as a heading. */
@Composable
fun SectionHeader(
    text: String,
    modifier: Modifier = Modifier,
    style: TextStyle = MaterialTheme.typography.titleMedium,
) {
    Text(text, style = style, modifier = modifier.semantics { heading() })
}
