package com.airlink.app

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.TimeUnit

class ApiException(message: String) : Exception(message)

data class UploadInit(
    val id: String,
    val chunkSize: Long,
    val totalChunks: Int,
    val received: BooleanArray,
)

class Api(private val device: Device) {

    private val jsonType = "application/json; charset=utf-8".toMediaType()
    private val binType = "application/octet-stream".toMediaType()

    private val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(4, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .writeTimeout(30, TimeUnit.SECONDS)
        .build()

    private fun builder(path: String) = Request.Builder()
        .url(device.base + path)
        .header("X-AirLink-Token", device.token)

    /** 握手，返回电脑名。配对码错误时抛异常。 */
    fun handshake(): String {
        val body = JSONObject().put("device", "android").toString().toRequestBody(jsonType)
        client.newCall(builder("/api/v1/handshake").post(body).build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("配对码无效")
            return JSONObject(r.body?.string().orEmpty()).optString("device", "电脑")
        }
    }

    fun listFiles(): List<FileItem> {
        client.newCall(builder("/api/v1/files").get().build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("读取文件列表失败")
            val arr = JSONObject(r.body?.string().orEmpty()).optJSONArray("files") ?: JSONArray()
            return (0 until arr.length()).map { i ->
                val o = arr.getJSONObject(i)
                FileItem(o.getString("id"), o.getString("name"), o.optLong("size"))
            }
        }
    }

    fun initUpload(name: String, size: Long): UploadInit {
        val body = JSONObject().put("name", name).put("size", size).toString().toRequestBody(jsonType)
        client.newCall(builder("/api/v1/upload/init").post(body).build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("初始化上传失败")
            val o = JSONObject(r.body?.string().orEmpty())
            val receivedJson = o.optJSONArray("received") ?: JSONArray()
            val received = BooleanArray(receivedJson.length()) { receivedJson.optBoolean(it, false) }
            return UploadInit(
                id = o.getString("id"),
                chunkSize = o.optLong("chunkSize", 4L * 1024 * 1024),
                totalChunks = o.optInt("totalChunks", 1),
                received = received,
            )
        }
    }

    fun putChunk(id: String, index: Int, data: ByteArray, length: Int) {
        val body = data.toRequestBody(binType, 0, length)
        client.newCall(builder("/api/v1/upload/$id/$index").put(body).build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("第 ${index + 1} 片上传失败")
        }
    }

    fun finishUpload(id: String) {
        val body = "{}".toRequestBody(jsonType)
        client.newCall(builder("/api/v1/upload/$id/finish").post(body).build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("合并文件失败")
        }
    }

    fun texts(): List<TextItem> {
        client.newCall(builder("/api/v1/text").get().build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("读取文本失败")
            val arr = JSONObject(r.body?.string().orEmpty()).optJSONArray("texts") ?: JSONArray()
            return (0 until arr.length()).map { i ->
                val o = arr.getJSONObject(i)
                TextItem(
                    id = o.optString("id"),
                    content = o.optString("content"),
                    from = o.optString("from"),
                    at = o.optString("at"),
                )
            }
        }
    }

    fun postText(content: String) {
        val body = JSONObject()
            .put("content", content)
            .put("from", "手机")
            .toString()
            .toRequestBody(jsonType)
        client.newCall(builder("/api/v1/text").post(body).build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("发送失败")
        }
    }

    fun clips(): List<ClipItem> {
        client.newCall(builder("/api/v1/clipboard").get().build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("读取剪贴板失败")
            val arr = JSONObject(r.body?.string().orEmpty()).optJSONArray("clips") ?: JSONArray()
            return (0 until arr.length()).map { i ->
                val o = arr.getJSONObject(i)
                ClipItem(
                    id = o.optString("id"),
                    content = o.optString("content"),
                    from = o.optString("from"),
                    at = o.optString("at"),
                )
            }
        }
    }

    fun postClip(content: String) {
        val body = JSONObject()
            .put("content", content)
            .put("from", "手机")
            .toString()
            .toRequestBody(jsonType)
        client.newCall(builder("/api/v1/clipboard").post(body).build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("发送剪贴板失败")
        }
    }

    /** 生成 4 位短链，24 小时有效、无提取码。 */
    fun createShare(fileId: String): String {
        val body = JSONObject()
            .put("fileId", fileId)
            .put("ttlHours", 24)
            .toString()
            .toRequestBody(jsonType)
        client.newCall(builder("/api/v1/shares").post(body).build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiException("生成分享链接失败")
            return JSONObject(r.body?.string().orEmpty()).optString("url")
        }
    }

    fun downloadUrl(fileId: String): String =
        "${device.base}/api/v1/files/$fileId?t=${device.token}"

    companion object {
        /** 探测某个候选地址是否真的可达。二维码里可能有好几个 IP，逐个试。 */
        fun probe(host: String, port: Int): Boolean {
            val quick = OkHttpClient.Builder()
                .connectTimeout(1500, TimeUnit.MILLISECONDS)
                .readTimeout(1500, TimeUnit.MILLISECONDS)
                .build()
            return try {
                quick.newCall(
                    Request.Builder().url("http://$host:$port/api/v1/ping").build()
                ).execute().use { it.isSuccessful }
            } catch (e: Exception) {
                false
            }
        }
    }
}

/** 把 "192.168.1.20:6027" 拆成 host / port。 */
fun splitHostPort(s: String): Pair<String, Int>? {
    val i = s.lastIndexOf(':')
    if (i <= 0) return null
    val port = s.substring(i + 1).toIntOrNull() ?: return null
    return s.substring(0, i) to port
}
