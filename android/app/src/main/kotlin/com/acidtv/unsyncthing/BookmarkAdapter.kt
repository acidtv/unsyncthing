package com.acidtv.unsyncthing

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import com.acidtv.unsyncthing.databinding.ItemBookmarkBinding

class BookmarkAdapter(
    private val onTap: (Bookmark) -> Unit,
    private val onMenuClick: (Bookmark, View) -> Unit,
) : ListAdapter<Bookmark, BookmarkAdapter.VH>(DIFF) {

    inner class VH(val binding: ItemBookmarkBinding) : RecyclerView.ViewHolder(binding.root)

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int) = VH(
        ItemBookmarkBinding.inflate(LayoutInflater.from(parent.context), parent, false)
    )

    override fun onBindViewHolder(holder: VH, position: Int) {
        val b = getItem(position)
        holder.binding.tvName.text = b.name
        holder.binding.tvDetails.text = "${b.folderID}  ·  ${b.peerID.take(7)}"
        holder.itemView.setOnClickListener { onTap(b) }
        holder.binding.ibMenu.setOnClickListener { v -> onMenuClick(b, v) }
    }

    companion object {
        private val DIFF = object : DiffUtil.ItemCallback<Bookmark>() {
            override fun areItemsTheSame(a: Bookmark, b: Bookmark) =
                a.peerID == b.peerID && a.folderID == b.folderID
            override fun areContentsTheSame(a: Bookmark, b: Bookmark) = a == b
        }
    }
}
