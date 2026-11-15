import { useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useState } from "react";
import { CreatePlaylistDialog } from "../../features/personal";
import { Button } from "../../shared/ui";

/** “新建歌单”（＋）：侧栏“我的歌单”一行与“我的”页共用歌单页的新建对话框，建好后打开这个歌单。 */
export function NewPlaylistButton() {
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);
  return (
    <>
      <Button variant="icon" icon={<Plus size={16} />} label="新建歌单" onClick={() => setCreating(true)} />
      <CreatePlaylistDialog
        open={creating}
        title="新建歌单"
        submitLabel="新建"
        onClose={() => setCreating(false)}
        onCreated={(result) => {
          setCreating(false);
          void navigate({ to: "/me/playlists/$playlistId", params: { playlistId: result.playlist.id } });
        }}
      />
    </>
  );
}
