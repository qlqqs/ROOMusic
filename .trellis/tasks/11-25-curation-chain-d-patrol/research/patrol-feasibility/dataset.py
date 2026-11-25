# 巡检可行性测试的带标注别名集：每条 (名字, 专辑样本, 身份标签)。
# 身份标签相同 = 同一身份；不同 = 不同身份（含名字相近的不同艺人与错字）。
import json, random

ROWS = [
    ("周杰伦", ["范特西", "叶惠美"], "jay"),
    ("周杰倫", ["八度空間", "七里香"], "jay"),
    ("Jay Chou", ["Fantasy", "November's Chopin"], "jay"),
    ("Chou Jay", ["Capricorn"], "jay"),
    ("王菲", ["浮躁", "唱游"], "faye"),
    ("Faye Wong", ["Fable", "To Love"], "faye"),
    ("王靖雯", ["Coming Home", "Everything"], "faye"),
    ("陈奕迅", ["U87", "黑白灰"], "eason"),
    ("陳奕迅", ["What's Going On...?", "Life Continues..."], "eason"),
    ("Eason Chan", ["The Easy Ride"], "eason"),
    ("张学友", ["吻别", "真情流露"], "jacky"),
    ("Jacky Cheung", ["The Year of Jacky Cheung"], "jacky"),
    ("邓丽君", ["淡淡幽情", "島國之情歌"], "teresa"),
    ("鄧麗君", ["償還"], "teresa"),
    ("Teresa Teng", ["Light Exquisite Feelings"], "teresa"),
    ("テレサ・テン", ["つぐない"], "teresa"),
    ("坂本龍一", ["音楽図鑑", "未来派野郎"], "sakamoto"),
    ("坂本龙一", ["BTTB"], "sakamoto"),
    ("Ryuichi Sakamoto", ["async", "Beauty"], "sakamoto"),
    ("Beyond", ["真的见证", "继续革命"], "beyond"),
    ("BEYOND", ["樂與怒"], "beyond"),
    ("达明一派", ["我等着你回来", "意难平"], "tatming"),
    ("Tat Ming Pair", ["The Story of Tat Ming"], "tatming"),
    ("李宗盛", ["生命中的精灵", "山丘"], "jonathan"),
    ("Jonathan Lee", ["Wandering Wind"], "jonathan"),
    ("Radiohead", ["OK Computer", "Kid A"], "radiohead"),
    ("Rdiohead", ["The Bends"], "radiohead"),
    ("Radio Head", ["Amnesiac"], "radiohead"),
    ("Björk", ["Debut", "Post"], "bjork"),
    ("Bjork", ["Homogenic"], "bjork"),
    ("björk", ["Vespertine"], "bjork"),
    ("The Beatles", ["Abbey Road", "Revolver"], "beatles"),
    ("Beatles", ["Rubber Soul"], "beatles"),
    ("披头士", ["白色专辑"], "beatles"),
    ("AC/DC", ["Back in Black", "Highway to Hell"], "acdc"),
    ("ACDC", ["Powerage"], "acdc"),
    ("Beyoncé", ["Lemonade", "4"], "beyonce"),
    ("Beyonce", ["Dangerously in Love"], "beyonce"),
    ("Bon Iver", ["For Emma, Forever Ago", "22, A Million"], "boniver"),
    ("Bon Jovi", ["Slippery When Wet", "New Jersey"], "bonjovi"),
    ("Prince", ["Purple Rain", "Sign o' the Times"], "prince"),
    ("Prince Royce", ["Phase II", "Soy el Mismo"], "princeroyce"),
    ("Sia", ["1000 Forms of Fear", "This Is Acting"], "sia"),
    ("Sade", ["Diamond Life", "Love Deluxe"], "sade"),
    ("陈慧娴", ["永远是你的朋友", "归来吧"], "priscilla"),
    ("Priscilla Chan", ["Priscilla"], "priscilla"),
    ("陈慧琳", ["记得我", "Kelly"], "kelly"),
    ("Kelly Chen", ["Love Fever"], "kelly"),
    ("林忆莲", ["野花", "铿锵玫瑰"], "sandy"),
    ("Sandy Lam", ["City Lights"], "sandy"),
    ("林憶蓮", ["夜太黑"], "sandy"),
    ("梅艳芳", ["似水流年", "坏女孩"], "anita"),
    ("Anita Mui", ["Bad Girl"], "anita"),
    ("周慧敏", ["最爱", "Vivian"], "vivian"),
    ("Vivian Chow", ["Endless Love"], "vivian"),
    ("Miles Davis", ["Kind of Blue", "Bitches Brew"], "miles"),
    ("Miles Davies", ["Sketches of Spain"], "miles"),
    ("John Coltrane", ["A Love Supreme", "Giant Steps"], "coltrane"),
    ("Alice Coltrane", ["Journey in Satchidananda"], "alicecoltrane"),
    ("Daft Punk", ["Discovery", "Homework"], "daftpunk"),
    ("Daft Punk", ["Random Access Memories"], "daftpunk"),
    ("宇多田ヒカル", ["First Love", "Distance"], "utada"),
    ("Utada Hikaru", ["Fantôme"], "utada"),
    ("Hikaru Utada", ["Exodus"], "utada"),
    ("椎名林檎", ["無罪モラトリアム", "勝訴ストリップ"], "shiina"),
    ("Sheena Ringo", ["Sanmon Gossip"], "shiina"),
]

def chunks(seed=7, n=4):
    rows = list(enumerate(ROWS))
    random.Random(seed).shuffle(rows)
    out = [[] for _ in range(n)]
    for i, r in enumerate(rows):
        out[i % n].append(r)
    return out

if __name__ == "__main__":
    import sys
    d = sys.argv[1]
    for i, c in enumerate(chunks()):
        lines = [{"id": idx, "name": name, "albums": albums} for idx, (name, albums, _) in c]
        json.dump(lines, open(f"{d}/chunk{i}.json", "w"), ensure_ascii=False)
    json.dump({i: r[2] for i, r in enumerate(ROWS)}, open(f"{d}/labels.json", "w"))
    print(len(ROWS), "rows;", len(set(r[2] for r in ROWS)), "identities")
