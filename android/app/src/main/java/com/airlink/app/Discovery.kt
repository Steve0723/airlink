package com.airlink.app

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import kotlinx.coroutines.delay
import java.util.Collections

data class Discovered(
    val deviceId: String,
    val name: String,
    val host: String,
    val port: Int,
)

/**
 * 用系统自带的 NsdManager 找局域网里的 AirLink，不引第三方库。
 *
 * 广播里只有设备 ID 和设备名，没有配对码——所以发现不等于能连。
 * 它的作用是：电脑 IP 变了（DHCP 重新分配、换了网络）时，
 * 手机能靠设备 ID 把新地址找回来，不用重新扫码。
 */
object Discovery {
    private const val SERVICE = "_airlink._tcp"

    suspend fun find(ctx: Context, timeoutMs: Long = 3500): List<Discovered> {
        val nsd = ctx.getSystemService(NsdManager::class.java) ?: return emptyList()
        val found = Collections.synchronizedList(mutableListOf<Discovered>())

        val listener = object : NsdManager.DiscoveryListener {
            override fun onServiceFound(info: NsdServiceInfo) {
                runCatching {
                    nsd.resolveService(info, object : NsdManager.ResolveListener {
                        override fun onResolveFailed(i: NsdServiceInfo?, errorCode: Int) = Unit

                        override fun onServiceResolved(i: NsdServiceInfo) {
                            val id = i.attributes?.get("d")?.toString(Charsets.UTF_8) ?: return
                            val host = i.host?.hostAddress ?: return
                            val name = i.attributes?.get("n")?.toString(Charsets.UTF_8).orEmpty()
                            found.add(Discovered(id, name, host, i.port))
                        }
                    })
                }
            }

            override fun onStartDiscoveryFailed(serviceType: String?, errorCode: Int) = Unit
            override fun onStopDiscoveryFailed(serviceType: String?, errorCode: Int) = Unit
            override fun onDiscoveryStarted(serviceType: String?) = Unit
            override fun onDiscoveryStopped(serviceType: String?) = Unit
            override fun onServiceLost(info: NsdServiceInfo?) = Unit
        }

        val started = runCatching {
            nsd.discoverServices(SERVICE, NsdManager.PROTOCOL_DNS_SD, listener)
        }.isSuccess
        if (!started) return emptyList()

        delay(timeoutMs)
        runCatching { nsd.stopServiceDiscovery(listener) }
        return found.toList()
    }
}
