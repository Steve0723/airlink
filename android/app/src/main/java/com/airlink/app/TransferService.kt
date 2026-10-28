package com.airlink.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.IBinder
import android.provider.OpenableColumns
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import java.io.InputStream

/** 界面和服务之间传递进度用的极简总线。 */
object TransferBus {
    val running = MutableStateFlow(false)
    val message = MutableStateFlow("")
    val finished = MutableStateFlow(0)
}

/**
 * 前台服务：锁屏、切后台都能继续传。
 * 安卓 14 起前台服务必须声明类型，这里用 dataSync。
 */
class TransferService : Service() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    override fun onBind(intent: Intent?): IBinder? = null

    // 用户点「中止发送」时，stopService 会走到这里，协程随之取消。
    override fun onDestroy() {
        scope.cancel()
        TransferBus.running.value = false
        TransferBus.message.value = "已中止发送"
        super.onDestroy()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val host = intent?.getStringExtra("host")
        val port = intent?.getIntExtra("port", 6027) ?: 6027
        val token = intent?.getStringExtra("token")
        val name = intent?.getStringExtra("name").orEmpty()
        val uris = intent?.getStringArrayListExtra("uris")?.map(Uri::parse).orEmpty()

        if (host == null || token == null || uris.isEmpty()) {
            stopSelf()
            return START_NOT_STICKY
        }

        createChannel()
        startForegroundCompat("准备传输…")
        TransferBus.running.value = true
        TransferBus.message.value = "正在发送 ${uris.size} 个文件"

        val device = Device(
            host = host,
            port = port,
            token = token,
            name = name,
            relayUrl = intent.getStringExtra("relayUrl").orEmpty(),
            relayKey = intent.getStringExtra("relayKey").orEmpty(),
            deviceId = intent.getStringExtra("deviceId").orEmpty(),
        )
        val api = Api(device)

        scope.launch {
            var ok = 0
            try {
                uris.forEachIndexed { i, uri ->
                    uploadOne(api, device, uri, i + 1, uris.size)
                    ok++
                }
                TransferBus.message.value = "已发送 $ok 个文件"
            } catch (e: Exception) {
                TransferBus.message.value = "传输中断：${e.message}"
            } finally {
                TransferBus.running.value = false
                TransferBus.finished.value += 1
                delay(500)
                ServiceCompat.stopForeground(this@TransferService, ServiceCompat.STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
        }
        return START_NOT_STICKY
    }

    // 先走局域网；连不上电脑时自动改走公网中转，用户不用做任何选择。
    private fun uploadOne(api: Api, device: Device, uri: Uri, index: Int, total: Int) {
        val (fileName, size) = describe(uri)
        try {
            lanUpload(api, uri, fileName, size, index, total)
        } catch (e: Exception) {
            if (!device.hasRelay) throw e
            notify("$fileName · 局域网不可达，改走公网…")
            val ok = api.relayPut(fileName, size) {
                contentResolver.openInputStream(uri) ?: throw e
            }
            if (!ok) throw ApiException("公网中转失败")
            TransferBus.message.value = "已通过公网中转发送，电脑开机后自动收到"
        }
    }

    private fun lanUpload(api: Api, uri: Uri, fileName: String, size: Long, index: Int, total: Int) {
        val init = api.initUpload(fileName, size)
        val buffer = ByteArray(init.chunkSize.toInt())
        var chunk = 0
        var sent = 0L

        contentResolver.openInputStream(uri)?.use { input ->
            while (true) {
                val read = readFully(input, buffer)
                if (read <= 0) break
                // 断点续传：服务端已收到的分片直接跳过
                if (chunk < init.received.size && init.received[chunk]) {
                    sent += read
                    chunk++
                    continue
                }
                api.putChunk(init.id, chunk, buffer, read)
                sent += read
                chunk++
                val pct = if (size > 0) ((sent * 100) / size).toInt() else 0
                notify("$fileName  $pct%   ($index/$total)")
            }
        }
        api.finishUpload(init.id)
    }

    private fun readFully(input: InputStream, buf: ByteArray): Int {
        var off = 0
        while (off < buf.size) {
            val n = input.read(buf, off, buf.size - off)
            if (n < 0) break
            off += n
        }
        return off
    }

    private fun describe(uri: Uri): Pair<String, Long> {
        var name = "file"
        var size = -1L
        contentResolver.query(uri, null, null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                val ni = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                val si = c.getColumnIndex(OpenableColumns.SIZE)
                if (ni >= 0) name = c.getString(ni) ?: name
                if (si >= 0 && !c.isNull(si)) size = c.getLong(si)
            }
        }
        if (size <= 0) {
            size = contentResolver.openAssetFileDescriptor(uri, "r")?.use { it.length } ?: 0L
        }
        return name to size
    }

    private fun createChannel() {
        val nm = getSystemService(NotificationManager::class.java)
        val ch = NotificationChannel(CHANNEL, "文件传输", NotificationManager.IMPORTANCE_LOW)
        ch.setShowBadge(false)
        nm.createNotificationChannel(ch)
    }

    private fun buildNotification(text: String): Notification {
        val pi = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return NotificationCompat.Builder(this, CHANNEL)
            .setContentTitle("AirLink")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(pi)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    private fun startForegroundCompat(text: String) {
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC
        } else {
            0
        }
        ServiceCompat.startForeground(this, NOTIF_ID, buildNotification(text), type)
    }

    private fun notify(text: String) {
        val nm = getSystemService(NotificationManager::class.java)
        nm.notify(NOTIF_ID, buildNotification(text))
    }

    companion object {
        private const val CHANNEL = "airlink_transfer"
        private const val NOTIF_ID = 1001

        fun cancel(ctx: Context) {
            ctx.stopService(Intent(ctx, TransferService::class.java))
        }

        fun start(ctx: Context, device: Device, uris: List<String>) {
            val i = Intent(ctx, TransferService::class.java).apply {
                putExtra("host", device.host)
                putExtra("port", device.port)
                putExtra("token", device.token)
                putExtra("name", device.name)
                putExtra("relayUrl", device.relayUrl)
                putExtra("relayKey", device.relayKey)
                putExtra("deviceId", device.deviceId)
                putStringArrayListExtra("uris", ArrayList(uris))
            }
            ContextCompat.startForegroundService(ctx, i)
        }
    }
}
