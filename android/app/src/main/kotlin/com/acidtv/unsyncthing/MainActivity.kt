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
        val current = supportFragmentManager.findFragmentById(R.id.fragmentContainer)
        if (current?.tag == tag) return
        // The preview sits on the back stack above the file list. A FileList
        // state update (e.g. a save-to-Downloads progress tick) must not
        // replace it — popping the preview restores the list from `_state`.
        if (current is PreviewFragment && tag == TAG_FILE_LIST) return
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
