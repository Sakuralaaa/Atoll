# WebView JS bridge
-keepclassmembers class io.atoll.app.web.AtollBridge {
    @android.webkit.JavascriptInterface <methods>;
}
-keepattributes JavascriptInterface
# Tink (security-crypto) references annotation classes that are not on the runtime classpath
-dontwarn com.google.errorprone.annotations.**
-dontwarn javax.annotation.**
-dontwarn com.google.api.client.http.**
-dontwarn org.joda.time.**
