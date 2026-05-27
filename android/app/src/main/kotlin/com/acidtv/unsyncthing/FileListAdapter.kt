package com.acidtv.unsyncthing

import android.view.LayoutInflater
import android.view.ViewGroup
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import com.acidtv.unsyncthing.databinding.ItemFileBinding
import java.text.CharacterIterator
import java.text.StringCharacterIterator

class FileListAdapter(
    private val onTap: (FileEntry) -> Unit,
) : ListAdapter<FileEntry, FileListAdapter.VH>(DIFF) {

    inner class VH(val binding: ItemFileBinding) : RecyclerView.ViewHolder(binding.root)

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int) = VH(
        ItemFileBinding.inflate(LayoutInflater.from(parent.context), parent, false)
    )

    override fun onBindViewHolder(holder: VH, position: Int) {
        val entry = getItem(position)
        holder.binding.tvName.text = entry.name
        holder.binding.tvMeta.text = if (entry.isDir) "folder" else humanReadableBytes(entry.size)
        holder.binding.tvIcon.text = if (entry.isDir) "📁" else "📄"
        holder.itemView.setOnClickListener { onTap(entry) }
    }

    companion object {
        private val DIFF = object : DiffUtil.ItemCallback<FileEntry>() {
            override fun areItemsTheSame(a: FileEntry, b: FileEntry) = a.path == b.path
            override fun areContentsTheSame(a: FileEntry, b: FileEntry) = a == b
        }
    }
}

internal fun humanReadableBytes(bytes: Long): String {
    if (bytes < 1000) return "$bytes B"
    val ci: CharacterIterator = StringCharacterIterator("kMGTPE")
    var v = bytes
    while (v >= 999_950) { v /= 1000; ci.next() }
    return "%.1f %cB".format(v / 1000.0, ci.current())
}
