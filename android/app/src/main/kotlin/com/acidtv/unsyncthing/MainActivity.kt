package com.acidtv.unsyncthing

import android.os.Bundle
import androidx.activity.viewModels
import androidx.appcompat.app.AppCompatActivity
import androidx.fragment.app.Fragment
import androidx.fragment.app.commit
import com.acidtv.unsyncthing.databinding.ActivityMainBinding

class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private val vm: SyncthingViewModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        vm.state.observe(this) { state ->
            showFragment(fragmentFor(state))
        }
    }

    private fun fragmentFor(state: UiState?): String = when (state) {
        is UiState.FileList -> TAG_FILE_LIST
        else -> TAG_CONNECT
    }

    private fun showFragment(tag: String) {
        if (supportFragmentManager.findFragmentById(R.id.fragmentContainer)?.tag == tag) return
        val fragment: Fragment = when (tag) {
            TAG_FILE_LIST -> FileListFragment()
            else -> ConnectFragment()
        }
        supportFragmentManager.commit {
            setReorderingAllowed(true)
            replace(R.id.fragmentContainer, fragment, tag)
        }
    }

    private companion object {
        const val TAG_CONNECT = "connect"
        const val TAG_FILE_LIST = "file_list"
    }
}
