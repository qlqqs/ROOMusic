import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { FolderListingDTO } from "../api/decoders";
import { fetchFolders } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

// 后端按字节序返回；这里按名字自然排序：“Disc 2”在“Disc 10”前，不分大小写，中文按拼音。
const collator = new Intl.Collator("zh-CN", { numeric: true, sensitivity: "base" });

function sorted(listing: FolderListingDTO): FolderListingDTO {
  return { ...listing, items: [...listing.items].sort((a, b) => collator.compare(a.name, b.name)) };
}

/** 一个文件夹的子文件夹（管理员）；path 为 null 是起点。 */
export function useFolderListing(path: string | null) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: libraryKeys.folders(generation, path),
    queryFn: ({ signal }) => fetchFolders(path, signal),
    select: (response) => sorted(response.data),
  });
}
