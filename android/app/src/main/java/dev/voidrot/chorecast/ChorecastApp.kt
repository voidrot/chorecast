package dev.voidrot.chorecast

import android.app.Application

class ChorecastApp : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
    }
}
