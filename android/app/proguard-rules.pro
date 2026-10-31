# Ktor references JVM-only classes that don't exist on Android; R8 would fail on them otherwise.
-dontwarn org.slf4j.impl.StaticLoggerBinder
-dontwarn java.lang.management.ManagementFactory
-dontwarn java.lang.management.RuntimeMXBean
# kotlinx.serialization ships its own R8 rules for @Serializable classes (incl. generated API models).
