plugins {
    id("com.android.application")
}

android {
    namespace = "life.integ.context"
    compileSdk = 37

    defaultConfig {
        applicationId = "life.integ.context"
        minSdk = 26
        targetSdk = 37
        versionCode = 1
        versionName = "0.1.0"

        // Optional Firebase project identifiers. No provider/service-account credentials belong in the APK.
        val firebase = mapOf(
            "google_app_id" to System.getenv("EDC_FIREBASE_APP_ID"),
            "google_api_key" to System.getenv("EDC_FIREBASE_API_KEY"),
            "project_id" to System.getenv("EDC_FIREBASE_PROJECT_ID"),
            "gcm_defaultSenderId" to System.getenv("EDC_FIREBASE_SENDER_ID")
        )
        require(firebase.values.all { it.isNullOrBlank() } || firebase.values.all { !it.isNullOrBlank() }) {
            "Set all four EDC_FIREBASE_* project identifiers or omit all four."
        }
        if (firebase.values.all { !it.isNullOrBlank() }) {
            firebase.forEach { (name, value) -> resValue("string", name, value!!) }
        }

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    implementation(platform("com.google.firebase:firebase-bom:34.11.0"))
    implementation("com.google.firebase:firebase-messaging")
    implementation("androidx.core:core:1.19.0")
    implementation("androidx.work:work-runtime:2.11.2")
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test:core:1.6.1")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test.uiautomator:uiautomator:2.3.0")
}
