package com.airlink.app

import android.content.Context

data class Device(
    val host: String,
    val port: Int,
    val token: String,
    val name: String,
) {
    val base: String get() = "http://$host:$port"
}

data class FileItem(val id: String, val name: String, val size: Long)

data class TextItem(val id: String, val content: String, val from: String, val at: String)

data class ClipItem(val id: String, val content: String, val from: String, val at: String)

object Prefs {
    private const val FILE = "airlink"

    fun load(ctx: Context): Device? {
        val sp = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        val host = sp.getString("host", null) ?: return null
        val token = sp.getString("token", null) ?: return null
        return Device(host, sp.getInt("port", 6027), token, sp.getString("name", "").orEmpty())
    }

    fun save(ctx: Context, d: Device) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit()
            .putString("host", d.host)
            .putInt("port", d.port)
            .putString("token", d.token)
            .putString("name", d.name)
            .apply()
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
