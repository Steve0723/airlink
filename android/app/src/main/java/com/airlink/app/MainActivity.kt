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
import androidx.compose.foundation.clickable
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
import androidx.compose.material3.AlertDialog
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.TextStyle
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

    var devices by remember { mutableStateOf(Prefs.loadAll(ctx)) }
    var active by remember { mutableStateOf<Device?>(null) }
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
                Prefs.upsert(ctx, found)
                devices = Prefs.loadAll(ctx)
                active = found
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

    val startScan = {
        scanLauncher.launch(
            ScanOptions().apply {
                setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                setPrompt("扫描电脑上的二维码")
                setBeepEnabled(false)
                setOrientationLocked(true) // 锁竖屏，跟着清单走
            }
        )
    }

    val current = active
    if (current == null) {
        DeviceListScreen(
            devices = devices,
            lastKey = Prefs.lastKey(ctx),
            connecting = connecting,
            error = error,
            onPick = { active = it },
            onScan = startScan,
            onDelete = { d ->
                Prefs.remove(ctx, d)
                devices = Prefs.loadAll(ctx)
            },
        )
    } else {
        HomeScreen(
            device = current,
            // 「返回」只回列表，配对信息保留——以前这里会清掉，等于每次都得重扫
            onDisconnect = { active = null },
            onDeviceUpdated = { updated ->
                Prefs.upsert(ctx, updated)
                devices = Prefs.loadAll(ctx)
                active = updated
            },
        )
    }
}

// 打开 App 先看到这里，不再每次都弹扫码。
@Composable
private fun DeviceListScreen(
    devices: List<Device>,
    lastKey: String,
    connecting: Boolean,
    error: String,
    onPick: (Device) -> Unit,
    onScan: () -> Unit,
    onDelete: (Device) -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(20.dp),
    ) {
        Spacer(Modifier.height(8.dp))
        Text("我的电脑", fontSize = 20.sp, fontWeight = FontWeight.SemiBold)
        Text(
            if (devices.isEmpty()) "还没有配对过电脑" else "点一台进入，点「移除」可取消配对",
            color = Muted,
            fontSize = 12.sp,
        )

        Spacer(Modifier.height(16.dp))

        LazyColumn(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            items(devices, key = { Prefs.key(it) }) { d ->
                Card {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(
                            Modifier
                                .weight(1f)
                                .clickable { onPick(d) }
                                .padding(vertical = 2.dp)
                        ) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Text(
                                    d.name.ifBlank { "电脑" },
                                    fontSize = 15.sp,
                                    fontWeight = FontWeight.Medium,
                                )
                                if (Prefs.key(d) == lastKey) {
                                    Spacer(Modifier.width(6.dp))
                                    Text("最近", color = AccentDark, fontSize = 10.sp)
                                }
                            }
                            Text("${d.host}:${d.port}", color = Muted, fontSize = 12.sp)
                            if (d.hasRelay) {
                                Text("公网兜底已开启", color = Muted, fontSize = 11.sp)
                            }
                        }
                        TextButton(onClick = { onDelete(d) }) {
                            Text("移除", color = Muted, fontSize = 13.sp)
                        }
                    }
                }
            }

            if (devices.isEmpty()) {
                item {
                    Text(
                        "在电脑上打开 AirLink，用下面的按钮扫它显示的二维码即可配对。",
                        color = Muted,
                        fontSize = 13.sp,
                    )
                }
            }
        }

        if (error.isNotEmpty()) {
            Text(error, color = Color(0xFFC0453F), fontSize = 12.sp)
            Spacer(Modifier.height(8.dp))
        }

        Button(
            onClick = onScan,
            modifier = Modifier
                .fillMaxWidth()
                .height(52.dp),
            shape = RoundedCornerShape(12.dp),
            enabled = !connecting,
        ) {
            Text(
                when {
                    connecting -> "正在连接…"
                    devices.isEmpty() -> "扫码配对电脑"
                    else -> "扫码添加新电脑"
                },
                fontSize = 15.sp,
            )
        }
        Spacer(Modifier.height(6.dp))
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
    var showSettings by remember { mutableStateOf(false) }
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

    Box(Modifier.fillMaxSize()) {
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
                    Text(
                        buildString {
                            append("${device.host}:${device.port}")
                            if (device.hasRelay) append(" · 公网兜底已开启")
                        },
                        color = Muted,
                        fontSize = 12.sp,
                    )
                }
                TextButton(onClick = { refreshKey++ }) { Text("刷新", fontSize = 13.sp) }
                TextButton(onClick = { showSettings = true }) { Text("设置", fontSize = 13.sp) }
                TextButton(onClick = onDisconnect) { Text("返回", fontSize = 13.sp) }
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
                        .height(46.dp),
                    shape = RoundedCornerShape(23.dp),
                ) { Text("发送手机剪贴板", fontSize = 14.sp) }

                Spacer(Modifier.height(10.dp))

                // 输入框做成和「发送」按钮同款胶囊：44 高、22 圆角。
                // 默认的 OutlinedTextField 是 Material 那套 56 高的描边盒子，
                // 在这个界面里显得又大又方。
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Surface(
                        shape = RoundedCornerShape(22.dp),
                        color = Color(0xFFF4F6F5),
                        border = androidx.compose.foundation.BorderStroke(1.dp, LineColor),
                        modifier = Modifier
                            .weight(1f)
                            .height(44.dp),
                    ) {
                        Box(
                            contentAlignment = Alignment.CenterStart,
                            modifier = Modifier.padding(horizontal = 16.dp),
                        ) {
                            BasicTextField(
                                value = textInput,
                                onValueChange = { textInput = it },
                                singleLine = true,
                                textStyle = TextStyle(fontSize = 14.sp, color = Color(0xFF171B19)),
                                cursorBrush = SolidColor(Accent),
                                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                                keyboardActions = KeyboardActions(onSend = {
                                    sendClipboard(scope, device, textInput) { textInput = "" }
                                }),
                                decorationBox = { inner ->
                                    if (textInput.isEmpty()) {
                                        Text("或手输一段文字", fontSize = 13.sp, color = Muted)
                                    }
                                    inner()
                                },
                            )
                        }
                    }
                    Spacer(Modifier.width(8.dp))
                    Button(
                        onClick = { sendClipboard(scope, device, textInput) { textInput = "" } },
                        modifier = Modifier.height(44.dp),
                        shape = RoundedCornerShape(22.dp),
                        contentPadding = PaddingValues(horizontal = 22.dp),
                        colors = ButtonDefaults.buttonColors(containerColor = Accent),
                    ) { Text("发送", fontSize = 14.sp) }
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
                    }) { Text("复制", color = AccentDark, fontSize = 13.sp) }
                    TextButton(onClick = {
                        scope.launch {
                            val ok = withContext(Dispatchers.IO) {
                                runCatching { Api(device).deleteClip(c.id) }.isSuccess
                            }
                            toast(ctx, if (ok) "已删除" else "删除失败")
                            refreshKey++
                        }
                    }) { Text("删", color = Muted, fontSize = 13.sp) }
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

    if (showSettings) {
        RelaySettingsDialog(
            device = device,
            onDismiss = { showSettings = false },
            onSave = { url, key ->
                onDeviceUpdated(device.copy(relayUrl = url, relayKey = key))
                showSettings = false
            },
        )
    }
    }
}

// 中转配置。以前只能在电脑上改、再重新扫码，手机上完全看不到状态。
@Composable
private fun RelaySettingsDialog(
    device: Device,
    onDismiss: () -> Unit,
    onSave: (String, String) -> Unit,
) {
    var url by remember { mutableStateOf(device.relayUrl) }
    var key by remember { mutableStateOf(device.relayKey) }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("公网中转", fontSize = 16.sp) },
        text = {
            Column {
                Text(
                    "手机离开同一个 WiFi 时，文件通过中转服务器发给电脑。两项都留空表示不用。",
                    color = Muted,
                    fontSize = 12.sp,
                )
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = url,
                    onValueChange = { url = it },
                    label = { Text("中转地址") },
                    placeholder = { Text("https://drop.example.com", fontSize = 12.sp) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(10.dp))
                OutlinedTextField(
                    value = key,
                    onValueChange = { key = it },
                    label = { Text("上传密钥") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(12.dp))
                Text(
                    "设备 ID：${device.deviceId.ifBlank { "未获取，请重新扫码" }}",
                    color = Muted,
                    fontSize = 11.sp,
                )
                if (device.deviceId.isBlank()) {
                    Spacer(Modifier.height(6.dp))
                    Text(
                        "设备 ID 由电脑生成，只能通过扫码获取。只填地址和密钥，兜底仍然用不了。",
                        color = Color(0xFFC0453F),
                        fontSize = 11.sp,
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { onSave(url.trim(), key.trim()) }) { Text("保存") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("取消") }
        },
    )
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
