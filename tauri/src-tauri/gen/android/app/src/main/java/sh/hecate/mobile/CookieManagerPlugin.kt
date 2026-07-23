package sh.hecate.mobile

import android.app.Activity
import android.webkit.CookieManager
import app.tauri.annotation.Command
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.Plugin

@TauriPlugin
class CookieManagerPlugin(activity: Activity) : Plugin(activity) {
  @Command
  fun clearAll(invoke: Invoke) {
    try {
      val cookieManager = CookieManager.getInstance()
      cookieManager.removeAllCookies {
        try {
          cookieManager.flush()
          invoke.resolve()
        } catch (error: Exception) {
          invoke.reject(error.message ?: "Could not flush the Android WebView cookie store.")
        }
      }
    } catch (error: Exception) {
      invoke.reject(error.message ?: "Could not clear the Android WebView cookie store.")
    }
  }
}
