package com.airlink.app

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject

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

    /** 设备在本地列表里的唯一键。优先用电脑的 deviceId，老数据没有就退回地址。 */
    fun key(d: Device): String = d.deviceId.ifBlank { "${d.host}:${d.port}" }

    fun loadAll(ctx: Context): List<Device> {
        val sp = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        val raw = sp.getString("devices", null)

        if (raw.isNullOrBlank()) {
            // 老版本只存了单台，迁移过来，别让已配对的用户重新扫码
            val single = legacyLoad(sp) ?: return emptyList()
            saveAll(ctx, listOf(single))
            sp.edit().putString("lastKey", key(single)).apply()
            return listOf(single)
        }

        return runCatching {
            val arr = JSONArray(raw)
            (0 until arr.length()).mapNotNull { i ->
                val o = arr.optJSONObject(i) ?: return@mapNotNull null
                val host = o.optString("host")
                val token = o.optString("token")
                if (host.isBlank() || token.isBlank()) return@mapNotNull null
                Device(
                    host = host,
                    port = o.optInt("port", 6027),
                    token = token,
                    name = o.optString("name"),
                    relayUrl = o.optString("relayUrl"),
                    relayKey = o.optString("relayKey"),
                    deviceId = o.optString("deviceId"),
                )
            }
        }.getOrDefault(emptyList())
    }

    fun saveAll(ctx: Context, list: List<Device>) {
        val arr = JSONArray()
        list.forEach { d ->
            arr.put(
                JSONObject()
                    .put("host", d.host)
                    .put("port", d.port)
                    .put("token", d.token)
                    .put("name", d.name)
                    .put("relayUrl", d.relayUrl)
                    .put("relayKey", d.relayKey)
                    .put("deviceId", d.deviceId)
            )
        }
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit()
            .putString("devices", arr.toString())
            .apply()
    }

    /** 按 key 更新或插入，并记为最近使用。扫码配对和 IP 自动修正都走这里。 */
    fun upsert(ctx: Context, d: Device) {
        val list = loadAll(ctx).toMutableList()
        val k = key(d)
        val i = list.indexOfFirst { key(it) == k }
        if (i >= 0) list[i] = d else list.add(d)
        saveAll(ctx, list)
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit()
            .putString("lastKey", k)
            .apply()
    }

    fun remove(ctx: Context, d: Device) {
        saveAll(ctx, loadAll(ctx).filterNot { key(it) == key(d) })
    }

    /** 上次连过的那台，用于列表里高亮。 */
    fun lastKey(ctx: Context): String =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString("lastKey", "").orEmpty()

    /** 手机自己的身份，第一次用到时生成。电脑靠它决定往哪投递。 */
    fun phoneID(ctx: Context): String {
        val sp = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        val existing = sp.getString("phoneId", null)
        if (!existing.isNullOrBlank()) return existing
        val id = java.util.UUID.randomUUID().toString().replace("-", "").take(16)
        sp.edit().putString("phoneId", id).apply()
        return id
    }

    /** 只清配对，保留 phoneID——否则电脑那边记的投递目标就对不上了。 */
    fun clearPairings(ctx: Context) {
        val sp = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        val pid = sp.getString("phoneId", null)
        sp.edit()
            .remove("devices")
            .remove("lastKey")
            .remove("host")
            .remove("port")
            .remove("token")
            .remove("name")
            .remove("relayUrl")
            .remove("relayKey")
            .remove("deviceId")
            .apply()
        if (pid != null) sp.edit().putString("phoneId", pid).apply()
    }

    private fun legacyLoad(sp: android.content.SharedPreferences): Device? {
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
