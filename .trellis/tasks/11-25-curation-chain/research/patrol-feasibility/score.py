import json, unicodedata, re, itertools
T2S = str.maketrans("倫陳嫻鄧麗憶蓮艷達龍", "伦陈娴邓丽忆莲艳达龙")
def norm(s):
    s = unicodedata.normalize("NFKC", s).translate(T2S).casefold()
    s = "".join(c for c in unicodedata.normalize("NFD", s) if unicodedata.category(c) != "Mn")
    return re.sub(r"[\W_]+", "", s)
labels = {int(k): v for k, v in json.load(open("labels.json")).items()}
for model in ["haiku", "sonnet"]:
    out = {}
    for i in range(4):
        for r in json.load(open(f"out_{model}_{i}.json")):
            orig, _, latin = r["canonical"].partition("|")
            out[r["id"]] = (norm(orig), norm(latin), r["confidence"])
    ids = sorted(out)
    for strategy in ["latin_else_orig", "orig_or_latin"]:
        parent = {i: i for i in ids}
        def find(x):
            while parent[x] != x: parent[x] = parent[parent[x]]; x = parent[x]
            return x
        for a, b in itertools.combinations(ids, 2):
            oa, la, _ = out[a]; ob, lb, _ = out[b]
            if strategy == "latin_else_orig":
                ka = la or oa; kb = lb or ob; same = ka == kb
            else:
                same = (oa and oa == ob) or (la and la == lb)
            if same: parent[find(a)] = find(b)
        merge = split = 0; mp = []; sp = []
        for a, b in itertools.combinations(ids, 2):
            same_pred = find(a) == find(b); same_true = labels[a] == labels[b]
            if same_pred and not same_true: merge += 1; mp.append((a, b))
            if same_true and not same_pred: split += 1; sp.append((a, b))
        true_pairs = sum(1 for a, b in itertools.combinations(ids, 2) if labels[a] == labels[b])
        diff_pairs = sum(1 for a, b in itertools.combinations(ids, 2) if labels[a] != labels[b])
        print(f"{model:6} {strategy:16} 并错 {merge}/{diff_pairs}  拆错 {split}/{true_pairs}  并错对={mp}  拆错对={sp}")
    low = [i for i in ids if out[i][2] == "low"]
    print(f"{model:6} low={low}")
