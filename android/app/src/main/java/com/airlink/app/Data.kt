package com.airlink.app

import android.content.Context

data class Device(
    val host: String,
    val port: Int,
    val token: String,
    val name: String,
    val relayUrl: String = "",
    val relayKey: String = "",
    val deviceId: String = "",
) {
    val base: String get() = "http://$host:$port"

    /** 二维码里带了中转信息，离开局域网时才能兜底。 */
    val hasRelay: Boolean
        get() = relayUrl.isNotBlank() && relayKey.isNotBlank() && deviceId.isNotBlank()
}

data class FileItem(val id: String, val name: String, val size: Long)

data class TextItem(val id: String, val content: String, val from: String, val at: String)

data class ClipItem(val id: String, val content: String, val from: String, val at: String)

/** 中转上等手机来取的一条文件。 */
data class RelayItem(val code: String, val name: String, val size: Long)

object Prefs {
    private const val FILE = "airlink"

    fun load(ctx: Context): Device? {
        val sp = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        val host = sp.getString("host", null) ?: return null
        val token = sp.getString("token", null) ?: return null
        return Device(
            host = host,
            port = sp.getInt("port", 6027),
            token = token,
            name = sp.getString("name", "").orEmpty(),
            relayUrl = sp.getString("relayUrl", "").orEmpty(),
            relayKey = sp.getString("relayKey", "").orEmpty(),
            deviceId = sp.getString("deviceId", "").orEmpty(),
        )
    }

    fun save(ctx: Context, d: Device) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit()
            .putString("host", d.host)
            .putInt("port", d.port)
            .putString("token", d.token)
            .putString("name", d.name)
            .putString("relayUrl", d.relayUrl)
            .putString("relayKey", d.relayKey)
            .putString("deviceId", d.deviceId)
            .apply()
    }

    /** 手机自己的身份，第一次用到时生成。电脑靠它决定往哪投递。 */
    fun phoneID(ctx: Context): String {
        val sp = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        val existing = sp.getString("phoneId", null)
        if (!existing.isNullOrBlank()) return existing
        val id = java.util.UUID.randomUUID().toString().replace("-", "").take(16)
        sp.edit().putString("phoneId", id).apply()
        return id
    }

    fun clear(ctx: Context) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().clear().apply()
    }
}

fun humanSize(n: Long): String {
    if (n < 1024) return "$n B"
    val units = arrayOf("KB", "MB", "GB", "TB")
    var v = n.toDouble()
    var i = -1
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024
        i++
    }
    return String.format("%.1f %s", v, units[i])
}
