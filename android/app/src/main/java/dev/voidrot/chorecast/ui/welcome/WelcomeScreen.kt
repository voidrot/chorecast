package dev.voidrot.chorecast.ui.welcome

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import dev.voidrot.chorecast.R

/** First launch: is this a kid's or family device (pair it), or a parent's phone (sign in)? */
@Composable
fun WelcomeScreen(onPair: () -> Unit, onParent: () -> Unit, modifier: Modifier = Modifier) {
    Column(
        modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(stringResource(R.string.welcome_title), style = MaterialTheme.typography.headlineMedium)
        Text(stringResource(R.string.welcome_body), style = MaterialTheme.typography.bodyLarge)
        Button(onClick = onPair, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.welcome_pair)) }
        Text(stringResource(R.string.welcome_pair_hint), style = MaterialTheme.typography.bodySmall)
        OutlinedButton(onClick = onParent, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.welcome_parent)) }
    }
}
