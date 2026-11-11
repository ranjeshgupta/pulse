# PulseFirebase calls FirebaseMessaging by reflection.
-keep class com.google.firebase.messaging.FirebaseMessaging { public *; }
-keep class com.google.android.gms.tasks.Task { public *; }
-keep interface com.google.android.gms.tasks.OnCompleteListener { *; }
