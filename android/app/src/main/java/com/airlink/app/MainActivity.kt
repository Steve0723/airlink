package com.airlink.app

import android.Manifest
import android.app.DownloadManager
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentUris
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.MediaStore
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private val Accent = Color(0xFF1D9E75)
private val AccentDark = Color(0xFF0F6E56)
private val PageBg = Color(0xFFF4F6F5)
private val CardBg = Color(0xFFFFFFFF)
private val LineColor = Color(0xFFE3E7E5)
private val Muted = Color(0xFF6B7370)

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            MaterialTheme(
                colorScheme = lightColorScheme(
                    primary = Accent,
                    onPrimary = Color.White,
                    background = PageBg,
                    surface = CardBg,
                )
            ) {
                Surface(modifier = Modifier.fillMaxSize(), color = PageBg) {
                    Root()
                }
            }
        }
    }
}

@Composable
private fun Root() {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()

    var device by remember { mutableStateOf(Prefs.load(ctx)) }
    var connecting by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }

    val scanLauncher = rememberLauncherForActivityResult(ScanContract()) { result ->
        val content = result.contents
        if (content.isNullOrBlank()) return@rememberLauncherForActivityResult
        connecting = true
        error = ""
        scope.launch {
            val found = withContext(Dispatchers.IO) { connectFromQr(content) }
            connecting = false
            if (found != null) {
                Prefs.save(ctx, found)
                device = found
            } else {
                error = "连不上电脑。请确认手机和电脑在同一个 WiFi，且电脑端 AirLink 正在运行。"
            }
        }
    }

    val notifPerm = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    LaunchedEffect(Unit) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            notifPerm.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    val current = device
    if (current == null) {
        ConnectScreen(
            connecting = connecting,
            error = error,
            onScan = {
                scanLauncher.launch(
                    ScanOptions().apply {
                        setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                        setPrompt("扫描电脑上的二维码")
                        setBeepEnabled(false)
                        setOrientationLocked(true) // 锁竖屏，跟着清单走
                    }
                )
            },
        )
    } else {
        HomeScreen(
            device = current,
            onDisconnect = {
                Prefs.clear(ctx)
                device = null
            },
            onDeviceUpdated = { updated ->
                Prefs.save(ctx, updated)
                device = updated
            },
        )
    }
}

@Composable
private fun ConnectScreen(connecting: Boolean, error: String, onScan: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(28.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Box(
            modifier = Modifier
                .size(64.dp)
                .background(Accent, RoundedCornerShape(18.dp)),
            contentAlignment = Alignment.Center,
        ) {
            Text("⇄", color = Color.White, fontSize = 30.sp, fontWeight = FontWeight.Bold)
        }
        Spacer(Modifier.height(22.dp))
        Text("AirLink", fontSize = 24.sp, fontWeight = FontWeight.Bold)
        Spacer(Modifier.height(8.dp))
        Text(
            "在电脑上打开 AirLink，用手机扫描屏幕上的二维码",
            color = Muted,
            fontSize = 14.sp,
            modifier = Modifier.padding(horizontal = 12.dp),
        )
        Spacer(Modifier.height(30.dp))

        Button(
            onClick = onScan,
            enabled = !connecting,
            modifier = Modifier
                .fillMaxWidth()
                .height(54.dp),
            shape = RoundedCornerShape(14.dp),
        ) {
            if (connecting) {
                CircularProgressIndicator(
                    modifier = Modifier.size(20.dp),
                    color = Color.White,
                    strokeWidth = 2.dp,
                )
                Spacer(Modifier.width(10.dp))
                Text("正在连接…", fontSize = 16.sp)
            } else {
                Text("扫码连接", fontSize = 16.sp)
            }
        }

        if (error.isNotEmpty()) {
            Spacer(Modifier.height(16.dp))
            Text(error, color = Color(0xFFC0453F), fontSize = 13.sp)
        }

        Spacer(Modifier.height(26.dp))
        Text(
            "不想装 App？直接用手机相机扫二维码，\n浏览器里也能传，只是没有后台续传。",
            color = Muted,
            fontSize = 12.sp,
        )
    }
}

@Composable
private fun HomeScreen(
    device: Device,
    onDisconnect: () -> Unit,
    onDeviceUpdated: (Device) -> Unit,
) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()

    var files by remember { mutableStateOf<List<FileItem>>(emptyList()) }
    var clips by remember { mutableStateOf<List<ClipItem>>(emptyList()) }
    var error by remember { mutableStateOf("") }
    var loading by remember { mutableStateOf(true) }
    var textInput by remember { mutableStateOf("") }

    val running by TransferBus.running.collectAsState()
    val status by TransferBus.message.collectAsState()
    val finished by TransferBus.finished.collectAsState()

    var inbox by remember { mutableStateOf<List<RelayItem>>(emptyList()) }
    var registered by remember { mutableStateOf(false) }
    var refreshKey by remember { mutableStateOf(0) }
    var lastDiscover by remember { mutableStateOf(0L) }

    // 电脑发来的剪贴板和文件没有推送通道，前台每 4 秒主动拉一次。
    LaunchedEffect(device) {
        while (true) {
            delay(4000)
            refreshKey++
        }
    }

    LaunchedEffect(device, finished, refreshKey) {
        val result = withContext(Dispatchers.IO) {
            runCatching {
                val api = Api(device)
                val f = api.listFiles()
                val c = api.clips()
                val b = api.relayInbox(Prefs.phoneID(ctx))
                Triple(f, c, b)
            }
        }
        // 连上之后上报一次手机身份，电脑才知道该往哪个 ID 投递
        if (result.isSuccess && !registered) {
            registered = true
            withContext(Dispatchers.IO) {
                Api(device).registerPhone(Prefs.phoneID(ctx), android.os.Build.MODEL)
            }
        }
        result.onSuccess { (f, c, b) ->
            files = f
            clips = c
            inbox = b
            error = ""
        }.onFailure {
            error = if (device.hasRelay) {
                "当前不在同一局域网，仍可发送（自动走公网中转）。"
            } else {
                "连接中断：${it.message}"
            }
            // 连不上时用 mDNS 找一下：电脑 IP 变了就自动换新地址，不用重新扫码
            val now = System.currentTimeMillis()
            if (device.deviceId.isNotBlank() && now - lastDiscover > 12_000) {
                lastDiscover = now
                val found = Discovery.find(ctx)
                    .firstOrNull { it.deviceId == device.deviceId }
                if (found != null && (found.host != device.host || found.port != device.port)) {
                    onDeviceUpdated(device.copy(host = found.host, port = found.port))
                    return@LaunchedEffect
                }
            }
        }
        loading = false
    }

    val pickLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments()
    ) { uris ->
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        uris.forEach { u ->
            runCatching {
                ctx.contentResolver.takePersistableUriPermission(
                    u, Intent.FLAG_GRANT_READ_URI_PERMISSION
                )
            }
        }
        TransferService.start(ctx, device, uris.map { it.toString() })
    }

    // 照片走系统选择器：用户自己挑，不擅自上传整个相册。
    // 安卓 11+ 用系统 Photo Picker，不需要相册权限。
    val photoPicker = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(200)
    ) { uris ->
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        TransferService.start(ctx, device, uris.map { it.toString() })
    }

    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(14.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text(device.name.ifBlank { "电脑" }, fontSize = 17.sp, fontWeight = FontWeight.SemiBold)
                    Text("${device.host}:${device.port}", color = Muted, fontSize = 12.sp)
                }
                TextButton(onClick = onDisconnect) { Text("断开") }
            }
        }

        item {
            Card {
                Button(
                    onClick = { pickLauncher.launch(arrayOf("*/*")) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(52.dp),
                    shape = RoundedCornerShape(12.dp),
                    enabled = !running,
                ) {
                    Text(if (running) "传输中…" else "选择文件发送到电脑", fontSize = 15.sp)
                }
                if (running) {
                    Spacer(Modifier.height(10.dp))
                    OutlinedButton(
                        onClick = { TransferService.cancel(ctx) },
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(44.dp),
                        shape = RoundedCornerShape(10.dp),
                    ) { Text("中止发送", fontSize = 14.sp, color = Color(0xFFC0453F)) }
                }
                if (status.isNotEmpty()) {
                    Spacer(Modifier.height(10.dp))
                    Text(status, color = Muted, fontSize = 12.sp)
                }
                if (error.isNotEmpty()) {
                    Spacer(Modifier.height(10.dp))
                    Text(error, color = Color(0xFFC0453F), fontSize = 12.sp)
                }
            }
        }

        item {
            Card {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text("选择照片", fontSize = 14.sp, fontWeight = FontWeight.Medium)
                        Text("从相册里挑，不会自动上传全部", color = Muted, fontSize = 12.sp)
                    }
                    OutlinedButton(
                        onClick = {
                            photoPicker.launch(
                                PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)
                            )
                        },
                        shape = RoundedCornerShape(9.dp),
                    ) { Text("选择", fontSize = 13.sp, color = AccentDark) }
                }
            }
        }

        if (inbox.isNotEmpty()) {
            item { SectionTitle("待接收", "${inbox.size} 个") }
            items(inbox, key = { it.code }) { r ->
                Card {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(
                                r.name,
                                fontSize = 14.sp,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            Text("来自电脑 · ${humanSize(r.size)}", color = Muted, fontSize = 12.sp)
                        }
                        Spacer(Modifier.width(10.dp))
                        OutlinedButton(
                            onClick = { downloadFromRelay(ctx, device, r) },
                            shape = RoundedCornerShape(9.dp),
                        ) {
                            Text("接收", fontSize = 13.sp, color = AccentDark)
                        }
                    }
                }
            }
        }

        item {
            SectionTitle("电脑上的文件", if (files.isEmpty()) "" else "${files.size} 个")
        }

        if (loading) {
            item {
                Card {
                    Text("正在读取…", color = Muted, fontSize = 13.sp)
                }
            }
        } else if (files.isEmpty()) {
            item {
                Card { Text("电脑上还没有文件", color = Muted, fontSize = 13.sp) }
            }
        } else {
            items(files, key = { it.id }) { f ->
                Card {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(
                                f.name,
                                fontSize = 14.sp,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            Text(humanSize(f.size), color = Muted, fontSize = 12.sp)
                        }
                        OutlinedButton(
                            onClick = { startDownload(ctx, device, f) },
                            shape = RoundedCornerShape(9.dp),
                        ) {
                            Text("下载", fontSize = 13.sp, color = AccentDark)
                        }
                    }
                }
            }
        }

        item { SectionTitle("剪贴板", "点「复制」取走") }

        item {
            Card {
                Button(
                    onClick = {
                        val text = readClipboard(ctx)
                        if (text.isBlank()) {
                            toast(ctx, "手机剪贴板里没有文本")
                        } else {
                            sendClipboard(scope, device, text) { toast(ctx, "已发送到电脑") }
                        }
                    },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(48.dp),
                    shape = RoundedCornerShape(12.dp),
                ) { Text("发送手机剪贴板", fontSize = 14.sp) }

                Spacer(Modifier.height(10.dp))

                Row(verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = textInput,
                        onValueChange = { textInput = it },
                        modifier = Modifier.weight(1f),
                        placeholder = { Text("或手输一段文字", fontSize = 13.sp) },
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                        keyboardActions = KeyboardActions(onSend = {
                            sendClipboard(scope, device, textInput) { textInput = "" }
                        }),
                    )
                    Spacer(Modifier.width(8.dp))
                    Button(
                        onClick = { sendClipboard(scope, device, textInput) { textInput = "" } },
                        shape = RoundedCornerShape(10.dp),
                        colors = ButtonDefaults.buttonColors(containerColor = Accent),
                    ) { Text("发送") }
                }
            }
        }

        items(clips.take(6), key = { it.id }) { c ->
            Card {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(c.content, fontSize = 13.sp, maxLines = 3, overflow = TextOverflow.Ellipsis)
                        Spacer(Modifier.height(4.dp))
                        Text(
                            "${c.from} · ${c.at.take(16).replace('T', ' ')}",
                            color = Muted,
                            fontSize = 11.sp,
                        )
                    }
                    Spacer(Modifier.width(8.dp))
                    TextButton(onClick = {
                        writeClipboard(ctx, c.content)
                        toast(ctx, "已复制到手机剪贴板")
                    }) { Text("复制", color = AccentDark) }
                }
            }
        }

        item {
            HorizontalDivider(color = LineColor)
            Spacer(Modifier.height(6.dp))
            Text(
                "后台传输由前台服务保活。若系统仍杀进程，请在设置里允许 AirLink 自启动、关闭电池优化。",
                color = Muted,
                fontSize = 11.sp,
            )
        }
    }
}

@Composable
private fun Card(content: @Composable () -> Unit) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(14.dp),
        color = CardBg,
        border = androidx.compose.foundation.BorderStroke(1.dp, LineColor),
    ) {
        Column(Modifier.padding(16.dp)) { content() }
    }
}

@Composable
private fun SectionTitle(title: String, trailing: String) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(title, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
        if (trailing.isNotEmpty()) Text(trailing, color = Muted, fontSize = 12.sp)
    }
}

private fun sendClipboard(
    scope: kotlinx.coroutines.CoroutineScope,
    device: Device,
    content: String,
    onDone: () -> Unit,
) {
    if (content.isBlank()) return
    scope.launch {
        val ok = withContext(Dispatchers.IO) {
            runCatching { Api(device).postClip(content.trim()) }.isSuccess
        }
        if (ok) onDone()
    }
}

// 安卓 10 起，应用只有在窗口有焦点时才能读剪贴板——用户点按钮时正好满足。
private fun readClipboard(ctx: Context): String {
    val cm = ctx.getSystemService(ClipboardManager::class.java) ?: return ""
    val clip = cm.primaryClip ?: return ""
    if (clip.itemCount == 0) return ""
    return clip.getItemAt(0).coerceToText(ctx)?.toString().orEmpty()
}

private fun writeClipboard(ctx: Context, text: String) {
    val cm = ctx.getSystemService(ClipboardManager::class.java) ?: return
    cm.setPrimaryClip(ClipData.newPlainText("AirLink", text))
}

private fun toast(ctx: Context, msg: String) {
    Toast.makeText(ctx, msg, Toast.LENGTH_SHORT).show()
}

// 从中转取文件。DownloadManager 支持自定义 header，密钥不用塞进 URL。
private fun downloadFromRelay(ctx: Context, device: Device, item: RelayItem) {
    val url = device.relayUrl.trimEnd('/') + "/api/v1/inbox/" + item.code
    val req = DownloadManager.Request(Uri.parse(url))
        .setTitle(item.name)
        .setDescription("来自电脑")
        .addRequestHeader("X-AirLink-Key", device.relayKey)
        .setMimeType("application/octet-stream")
        .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
    ctx.getSystemService(DownloadManager::class.java).enqueue(req)
}

private fun startDownload(ctx: Context, device: Device, f: FileItem) {
    val url = "${device.base}/api/v1/files/${f.id}?t=${device.token}"
    val req = DownloadManager.Request(Uri.parse(url))
        .setTitle(f.name)
        .setDescription("来自 ${device.name}")
        .setMimeType("application/octet-stream")
        .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
    ctx.getSystemService(DownloadManager::class.java).enqueue(req)
}

/** 二维码里带的是候选地址列表，逐个探测，第一个通的就用。 */
private fun connectFromQr(raw: String): Device? {
    val uri = runCatching { Uri.parse(raw) }.getOrNull() ?: return null
    val token = uri.getQueryParameter("t") ?: return null
    val name = uri.getQueryParameter("n").orEmpty()
    val relayUrl = uri.getQueryParameter("r").orEmpty()
    val relayKey = uri.getQueryParameter("k").orEmpty()
    val deviceId = uri.getQueryParameter("d").orEmpty()

    val candidates = LinkedHashSet<String>()
    uri.getQueryParameter("h")?.split(",")?.forEach { if (it.isNotBlank()) candidates.add(it.trim()) }
    val host = uri.host
    val port = uri.port
    if (host != null && port > 0) candidates.add("$host:$port")

    for (hp in candidates) {
        val (h, p) = splitHostPort(hp) ?: continue
        if (!Api.probe(h, p)) continue
        val dev = Device(h, p, token, name, relayUrl, relayKey, deviceId)
        if (runCatching { Api(dev).handshake() }.isSuccess) return dev
    }
    return null
}
