package dev.code04.lastro.ui

import androidx.activity.compose.BackHandler
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
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import dev.code04.lastro.R
import dev.code04.lastro.data.AppRepository
import dev.code04.lastro.history.CollectionStages
import dev.code04.lastro.history.History
import dev.code04.lastro.net.ExpenseQuery
import dev.code04.lastro.net.ServerNotification
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import java.time.YearMonth

enum class Tab(val label: String, @param:DrawableRes val icon: Int) {
    OVERVIEW("Visão geral", R.drawable.ic_tab_overview),
    EXPENSES("Despesas", R.drawable.ic_tab_expenses),
    ANALYSIS("Análises", R.drawable.ic_tab_analysis),
    COLLECTION("Coleta", R.drawable.ic_tab_collection),
    ;

    fun root(): Route = when (this) {
        OVERVIEW -> Route.Overview
        EXPENSES -> Route.Expenses(ExpenseQuery(month = YearMonth.now().toString()))
        ANALYSIS -> Route.Analysis
        COLLECTION -> Route.Collection
    }
}

sealed interface Route {
    data object Overview : Route

    data class Expenses(val query: ExpenseQuery) : Route

    data class Expense(val id: String) : Route

    data object Analysis : Route

    data object Collection : Route

    data object Settings : Route
}

data class ServerView(
    val notifications: List<ServerNotification>? = null,
    val checkedAt: Long = 0,
    val checking: Boolean = false,
    val problem: String = "",
)

@Composable
fun LastroApp(dashboard: Dashboard, repository: AppRepository, actions: SettingsActions) {
    var tab by rememberSaveable { mutableStateOf(Tab.OVERVIEW) }
    var stack by remember { mutableStateOf(listOf(tab.root())) }
    var snapshot by remember { mutableStateOf(dashboard.snapshot()) }
    var captures by remember { mutableStateOf(dashboard.captures()) }
    var server by remember { mutableStateOf(ServerView()) }
    var refreshKey by remember { mutableIntStateOf(0) }
    val scope = rememberCoroutineScope()
    var check by remember { mutableStateOf<Job?>(null) }

    val refreshLocal: () -> Unit = {
        snapshot = dashboard.snapshot()
        captures = dashboard.captures()
    }
    val refresh: () -> Unit = {
        refreshLocal()
        refreshKey++
        check?.cancel()
        check = scope.launch {
            server = server.copy(checking = true)
            server = when (val fetch = dashboard.serverNotifications()) {
                is ServerFetch.Done -> ServerView(fetch.notifications, System.currentTimeMillis())
                is ServerFetch.Failed -> server.copy(checking = false, problem = fetch.message)
                ServerFetch.NotConfigured -> ServerView(problem = "Configure o servidor e a credencial em Configurações para ver a situação na Case.")
            }
        }
    }
    LifecycleResumeEffect(Unit) {
        refresh()
        onPauseOrDispose { }
    }
    val items = remember(captures, server.notifications) { History.merge(captures, server.notifications) }
    val glance = collectionGlance(snapshot, remember(items) { CollectionStages.summarize(items, collectionSince()) })
    val route = stack.last()
    val navigate: (Route) -> Unit = { stack = stack + it }
    val back: () -> Unit = { stack = stack.dropLast(1) }
    val selectTab: (Tab) -> Unit = {
        tab = it
        stack = listOf(it.root())
    }
    val openSettings = { navigate(Route.Settings) }
    BackHandler(enabled = stack.size > 1, onBack = back)

    CompositionLocalProvider(LocalHidden provides snapshot.hideValues) {
        Scaffold(
            topBar = {
                TopBar(
                    title = titleOf(route),
                    canGoBack = stack.size > 1,
                    root = stack.size == 1,
                    hidden = snapshot.hideValues,
                    showEye = route != Route.Settings,
                    checking = server.checking,
                    onBack = back,
                    onToggleHidden = {
                        actions.setHideValues(!snapshot.hideValues)
                        refreshLocal()
                    },
                    onSettings = openSettings,
                    onRefresh = refresh,
                )
            },
            bottomBar = {
                NavigationBar {
                    Tab.entries.forEach { entry ->
                        NavigationBarItem(
                            selected = tab == entry,
                            onClick = { selectTab(entry) },
                            icon = { Icon(painterResource(entry.icon), contentDescription = null) },
                            label = { Text(entry.label, maxLines = 1) },
                        )
                    }
                }
            },
        ) { padding ->
            val modifier = Modifier.padding(padding)
            when (route) {
                Route.Overview -> OverviewScreen(
                    repository = repository,
                    snapshot = snapshot,
                    glance = glance,
                    refreshKey = refreshKey,
                    onOpenList = { navigate(Route.Expenses(it)) },
                    onOpenCollection = { selectTab(Tab.COLLECTION) },
                    onOpenSettings = openSettings,
                    modifier = modifier,
                )
                is Route.Expenses -> ExpensesScreen(
                    repository = repository,
                    initial = route.query,
                    refreshKey = refreshKey,
                    onOpenExpense = { navigate(Route.Expense(it)) },
                    onOpenSettings = openSettings,
                    modifier = modifier,
                )
                is Route.Expense -> ExpenseDetailScreen(
                    repository = repository,
                    id = route.id,
                    refreshKey = refreshKey,
                    onOpenCollection = { selectTab(Tab.COLLECTION) },
                    onOpenSettings = openSettings,
                    modifier = modifier,
                )
                Route.Analysis -> AnalysisScreen(
                    repository = repository,
                    refreshKey = refreshKey,
                    onOpenList = { navigate(Route.Expenses(it)) },
                    onOpenOverview = { selectTab(Tab.OVERVIEW) },
                    onOpenSettings = openSettings,
                    modifier = modifier,
                )
                Route.Collection -> CollectionScreen(
                    snapshot = snapshot,
                    items = items,
                    server = server,
                    onOpenExpense = { navigate(Route.Expense(it)) },
                    onOpenSettings = openSettings,
                    modifier = modifier,
                )
                Route.Settings -> SettingsScreen(
                    snapshot = snapshot,
                    repository = repository,
                    actions = actions,
                    onChanged = refresh,
                    modifier = modifier,
                )
            }
        }
    }
}

private fun titleOf(route: Route): String = when (route) {
    Route.Overview -> "Visão geral"
    is Route.Expenses -> route.query.label ?: "Despesas"
    is Route.Expense -> "Despesa"
    Route.Analysis -> "Análises"
    Route.Collection -> "Coleta"
    Route.Settings -> "Configurações"
}

@Composable
private fun TopBar(
    title: String,
    canGoBack: Boolean,
    root: Boolean,
    hidden: Boolean,
    showEye: Boolean,
    checking: Boolean,
    onBack: () -> Unit,
    onToggleHidden: () -> Unit,
    onSettings: () -> Unit,
    onRefresh: () -> Unit,
) {
    Row(
        Modifier.fillMaxWidth().statusBarsPadding().padding(start = 4.dp, end = 4.dp, top = 8.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (canGoBack) {
            IconButton(onClick = onBack) { TopIcon(R.drawable.ic_back, "Voltar") }
        } else {
            Spacer(Modifier.width(12.dp))
            Image(painterResource(R.drawable.ic_logo), contentDescription = null, modifier = Modifier.size(28.dp))
            Spacer(Modifier.width(12.dp))
        }
        Text(title, style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (showEye) {
            IconButton(
                onClick = onToggleHidden,
                colors = if (hidden) IconButtonDefaults.filledTonalIconButtonColors() else IconButtonDefaults.iconButtonColors(),
            ) {
                TopIcon(if (hidden) R.drawable.ic_eye_off else R.drawable.ic_eye, if (hidden) "Mostrar valores" else "Ocultar valores")
            }
        }
        if (root) {
            if (checking) {
                CircularProgressIndicator(Modifier.padding(12.dp).size(24.dp), strokeWidth = 2.dp)
            } else {
                IconButton(onClick = onRefresh) { TopIcon(R.drawable.ic_refresh, "Atualizar") }
            }
            IconButton(onClick = onSettings) { TopIcon(R.drawable.ic_sliders, "Configurações") }
        }
    }
}

@Composable
private fun TopIcon(@DrawableRes icon: Int, description: String) {
    Icon(painterResource(icon), contentDescription = description, tint = MaterialTheme.colorScheme.onSurfaceVariant)
}
