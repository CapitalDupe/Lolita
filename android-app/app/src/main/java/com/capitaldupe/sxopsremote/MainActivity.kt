package com.capitaldupe.sxopsremote

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

data class ComponentStatus(val name: String, val state: String, val running: Boolean, val version: String, val address: String, val onion: String)

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { MaterialTheme { Surface(Modifier.fillMaxSize()) { RemoteScreen() } } }
    }
}

@Composable
private fun RemoteScreen() {
    var endpoint by remember { mutableStateOf("https://your-server.example:8787") }
    var token by remember { mutableStateOf("") }
    var statuses by remember { mutableStateOf<List<ComponentStatus>>(emptyList()) }
    var message by remember { mutableStateOf("Connect to your sxops host over HTTPS.") }
    var busy by remember { mutableStateOf(false) }
    var confirm by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()

    fun run(action: String) {
        scope.launch {
            busy = true
            try {
                val base = endpoint.trim().trimEnd('/')
                require(base.startsWith("https://", ignoreCase = true)) { "Use an HTTPS URL with a trusted certificate." }
                require(token.length >= 32) { "Enter the host bearer token (at least 32 characters)." }
                val response = withContext(Dispatchers.IO) { request(base, token, action) }
                if (action == "status") {
                    statuses = parseStatuses(response)
                    message = "Connected · ${statuses.count { it.running }} component(s) running"
                } else {
                    val refreshed = withContext(Dispatchers.IO) { request(base, token, "status") }
                    statuses = parseStatuses(refreshed)
                    message = if (action == "start") "Start request completed." else "Stop request completed; Tor remains running."
                }
            } catch (e: Exception) {
                message = e.message ?: "Request failed. Check the URL, token, certificate, and host logs."
            } finally { busy = false; confirm = "" }
        }
    }

    if (confirm.isNotEmpty()) AlertDialog(
        onDismissRequest = { confirm = "" },
        title = { Text(if (confirm == "start") "Start sxops servers?" else "Stop sxops servers?") },
        text = { Text(if (confirm == "start") "Start all enabled server components on the remote host." else "Stop server components on the remote host. Tor will remain running.") },
        confirmButton = { TextButton(onClick = { run(confirm) }) { Text("Confirm") } },
        dismissButton = { TextButton(onClick = { confirm = "" }) { Text("Cancel") } }
    )

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("sxops Remote", style = MaterialTheme.typography.headlineMedium)
        Text("Secure companion · remote host controls")
        OutlinedTextField(endpoint, { endpoint = it }, label = { Text("Host URL (HTTPS)") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(token, { token = it }, label = { Text("Bearer token") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(enabled = !busy, onClick = { run("status") }) { Text("Refresh") }
            Button(enabled = !busy, onClick = { confirm = "start" }) { Text("Start") }
            Button(enabled = !busy, onClick = { confirm = "stop" }) { Text("Stop") }
        }
        if (busy) CircularProgressIndicator()
        Text(message, style = MaterialTheme.typography.bodyMedium)
        statuses.forEach { item -> Card(Modifier.fillMaxWidth()) {
            Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(item.name, style = MaterialTheme.typography.titleMedium)
                Text(if (item.running) "Running" else item.state)
                if (item.version.isNotBlank()) Text("Version: ${item.version}")
                if (item.address.isNotBlank()) Text("Address: ${item.address}")
                if (item.onion.isNotBlank()) Text("Onion: ${item.onion}")
            }
        } }
        Spacer(Modifier.height(8.dp))
        Text("HTTPS and a long bearer token are required. The token stays in memory only. Do not expose the API without a firewall.", style = MaterialTheme.typography.bodySmall)
    }
}

private fun request(base: String, token: String, action: String): JSONObject {
    val connection = URL("$base/api/v1/$action").openConnection() as HttpURLConnection
    try {
        connection.requestMethod = if (action == "status") "GET" else "POST"
        connection.connectTimeout = 10_000
        connection.readTimeout = 300_000
        connection.setRequestProperty("Authorization", "Bearer $token")
        connection.setRequestProperty("Accept", "application/json")
        val code = connection.responseCode
        val stream = if (code in 200..299) connection.inputStream else connection.errorStream
        val body = stream?.bufferedReader()?.use { it.readText() }.orEmpty()
        if (code !in 200..299) throw IllegalStateException(runCatching { JSONObject(body).optString("error", "HTTP $code") }.getOrDefault("HTTP $code"))
        return JSONObject(body)
    } finally { connection.disconnect() }
}

private fun parseStatuses(json: JSONObject): List<ComponentStatus> {
    val items = json.optJSONArray("components") ?: return emptyList()
    return (0 until items.length()).map { i ->
        val item = items.getJSONObject(i)
        ComponentStatus(item.optString("name"), item.optString("state"), item.optBoolean("running"), item.optString("version"), item.optString("address"), item.optString("onionHost"))
    }
}
