package dev.code04.lastro.ui

import androidx.annotation.DrawableRes
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import dev.code04.lastro.R
import dev.code04.lastro.history.History
import dev.code04.lastro.net.ServerNotification
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

enum class Tab(val label: String, @param:DrawableRes val icon: Int) {
    HOME("Início", R.drawable.ic_tab_home),
    NOTIFICATIONS("Notificações", R.drawable.ic_tab_notifications),
    SETTINGS("Ajustes", R.drawable.ic_tab_settings),
}

data class ServerView(
    val notifications: List<ServerNotification>? = null,
    val checkedAt: Long = 0,
    val checking: Boolean = false,
    val problem: String = "",
)

@Composable
fun LastroApp(dashboard: Dashboard, actions: SettingsActions) {
    var tab by rememberSaveable { mutableStateOf(Tab.HOME) }
    var snapshot by remember { mutableStateOf(dashboard.snapshot()) }
    var captures by remember { mutableStateOf(dashboard.captures()) }
    var server by remember { mutableStateOf(ServerView()) }
    val scope = rememberCoroutineScope()
    var check by remember { mutableStateOf<Job?>(null) }

    val refresh: () -> Unit = {
        snapshot = dashboard.snapshot()
        captures = dashboard.captures()
        check?.cancel()
        check = scope.launch {
            server = server.copy(checking = true)
            server = when (val fetch = dashboard.serverNotifications()) {
                is ServerFetch.Done -> ServerView(fetch.notifications, System.currentTimeMillis())
                is ServerFetch.Failed -> server.copy(checking = false, problem = fetch.message)
                ServerFetch.NotConfigured -> ServerView(problem = "Configure o servidor e a chave em Ajustes para ver a situação na Case.")
            }
        }
    }
    LifecycleResumeEffect(Unit) {
        refresh()
        onPauseOrDispose { }
    }
    val items = remember(captures, server.notifications) { History.merge(captures, server.notifications) }

    Scaffold(
        topBar = { TopBar(checking = server.checking, onRefresh = refresh) },
        bottomBar = {
            NavigationBar {
                Tab.entries.forEach { entry ->
                    NavigationBarItem(
                        selected = tab == entry,
                        onClick = { tab = entry },
                        icon = { Icon(painterResource(entry.icon), contentDescription = null) },
                        label = { Text(entry.label) },
                    )
                }
            }
        },
    ) { padding ->
        val modifier = Modifier.padding(padding)
        when (tab) {
            Tab.HOME -> HomeScreen(snapshot, items, server, onOpenNotifications = { tab = Tab.NOTIFICATIONS }, modifier = modifier)
            Tab.NOTIFICATIONS -> NotificationsScreen(items, server, modifier)
            Tab.SETTINGS -> SettingsScreen(snapshot, actions, onChanged = refresh, modifier = modifier)
        }
    }
}

@Composable
private fun TopBar(checking: Boolean, onRefresh: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().statusBarsPadding().padding(start = 16.dp, end = 4.dp, top = 8.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Image(painterResource(R.drawable.ic_logo), contentDescription = null, modifier = Modifier.size(32.dp))
        Spacer(Modifier.width(12.dp))
        Text("Lastro", style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
        if (checking) {
            CircularProgressIndicator(Modifier.padding(12.dp).size(24.dp), strokeWidth = 2.dp)
        } else {
            IconButton(onClick = onRefresh) {
                Icon(painterResource(R.drawable.ic_refresh), contentDescription = "Atualizar")
            }
        }
    }
}
