package dev.code04.lastro.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import dev.code04.lastro.data.Fetch
import dev.code04.lastro.data.Problem

sealed interface Load<out T> {
    data object Loading : Load<Nothing>

    data class Ready<T>(val value: T) : Load<T>

    data class Failed(val problem: Problem) : Load<Nothing>
}

class Loaded<T>(val state: Load<T>, val retry: () -> Unit)

@Composable
fun <T> load(vararg keys: Any?, fetch: suspend () -> Fetch<T>): Loaded<T> {
    var attempt by remember { mutableIntStateOf(0) }
    var state by remember(*keys) { mutableStateOf<Load<T>>(Load.Loading) }
    LaunchedEffect(*keys, attempt) {
        state = when (val result = fetch()) {
            is Fetch.Ok -> Load.Ready(result.value)
            is Fetch.Failed -> Load.Failed(result.problem)
        }
    }
    return Loaded(state) { attempt++ }
}
