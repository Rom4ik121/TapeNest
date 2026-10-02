#!/usr/bin/env python3
"""DEV ONLY: seed MUSIC_DIR with a small demo library of legally free recordings.

Source: Wikimedia Commons, category "Musopen" (Musopen recordings released as CC0 /
public domain). The license of every file is re-checked through the Commons API at
download time; anything that is not CC0 / Public domain is refused. Files are
converted to MP3 (universal <audio> support incl. iOS WebView) with ID3 tags, and
every album folder gets a generated gradient cover in the TapeNest palette.
A CREDITS.md with source URLs and licenses is written next to the music.

Stage "reco" adds catalog discovery (``--extra``, default on): more CC0 / public-domain
recordings are discovered in a few Commons categories (Musopen classical, early ragtime,
early jazz, folk, marches …). Discovery is fully automatic but every file still goes
through the same licence gate (extmetadata LicenseShortName must be exactly "CC0" or
"Public domain"), a size gate and a duration gate (45 s … 8 min). Each category maps to a
real ID3 genre so the recommender has genre signal. Titles/artists are parsed from the
Commons file names (best effort, documented in CREDITS.md next to the source URL).

Usage: seed-music.py [MUSIC_DIR] [--core-only] [--extra-limit N]
       (default dir: $MUSIC_DIR or ./data/music — the compose /music volume; N default 130)
Idempotent: existing MP3s are kept; downloads are cached in MUSIC_DIR/.cache.
"""
import html
import json
import os
import re
import subprocess
import sys
import time
import unicodedata
import urllib.parse
import urllib.request

UA = "TapeNestDev/0.4 (demo seed; https://github.com/tapenest)"
API = "https://commons.wikimedia.org/w/api.php"
ALLOWED = {"CC0", "Public domain"}

# (Commons file title, artist, album, track title, track no)
MANIFEST = [
    ("Albinoni, Concerto for Oboe and Strings No. 2 in D minor, Op. 9, I. Allegro e con presto.ogg",
     "Tomaso Albinoni", "Oboe Concerto Op. 9 No. 2", "I. Allegro e con presto", 1),
    ("Albinoni, Concerto for Oboe and Strings No. 2 in D minor, Op. 9, II. Adagio.ogg",
     "Tomaso Albinoni", "Oboe Concerto Op. 9 No. 2", "II. Adagio", 2),
    ("Albinoni, Concerto for Oboe and Strings No. 2 in D minor, Op. 9, III. Allegro.ogg",
     "Tomaso Albinoni", "Oboe Concerto Op. 9 No. 2", "III. Allegro", 3),
    ("Bach, Johann Sebastian - Suite No.2 in B Minor - X. Badinerie.ogg",
     "Johann Sebastian Bach", "Bach Favourites", "Suite No. 2 — Badinerie", 1),
    ("Bach, BWV 147, 10. Jesus bleibet meine Freude.ogg",
     "Johann Sebastian Bach", "Bach Favourites", "Jesu, Joy of Man's Desiring", 2),
    ("Bach, Goldberg Variations, Aria (Musopen version).ogg",
     "Johann Sebastian Bach", "Bach Favourites", "Goldberg Variations — Aria", 3),
    ("Chopin Nocturne No. 2 in E Flat Major, Op. 9.ogg",
     "Frédéric Chopin", "Chopin Piano Pieces", "Nocturne Op. 9 No. 2", 1),
    ("Chopin - Waltz op. 34 no 3.ogg",
     "Frédéric Chopin", "Chopin Piano Pieces", "Waltz Op. 34 No. 3", 2),
    ("Chopin - Mazurka-op-50-no-1.ogg",
     "Frédéric Chopin", "Chopin Piano Pieces", "Mazurka Op. 50 No. 1", 3),
    ("Clementi, Sonatina N.1 - 1 Mov. Allegro.ogg",
     "Muzio Clementi", "Sonatina Op. 36 No. 1", "I. Allegro", 1),
    ("Clementi, Sonatina N.1 - 2 Mov. Andante.ogg",
     "Muzio Clementi", "Sonatina Op. 36 No. 1", "II. Andante", 2),
    ("Clementi, Sonatina N.1 - 3 Mov. Vivace.ogg",
     "Muzio Clementi", "Sonatina Op. 36 No. 1", "III. Vivace", 3),
    ("Grieg, Peer Gynt Suite No. 1, Op. 46 - III. Anitra's Dance.ogg",
     "Edvard Grieg", "Classical Miniatures", "Peer Gynt — Anitra's Dance", 1),
    ("Mozart, The Marriage of Figaro (overture).ogg",
     "Wolfgang Amadeus Mozart", "Classical Miniatures", "The Marriage of Figaro — Overture", 2),
    ("Johann Strauss - Wiener Blut Op. 354.ogg",
     "Johann Strauss II", "Classical Miniatures", "Wiener Blut Op. 354", 3),
    ("Handel - Suites for Harpsichord - No.5 in E major - The Harmonious Blacksmith.ogg",
     "George Frideric Handel", "Classical Miniatures", "The Harmonious Blacksmith", 4),
]

ERA = {
    "Baroque": ["Bach", "Handel", "Vivaldi", "Albinoni", "Telemann", "Purcell", "Pachelbel", "Corelli",
                "Scarlatti", "Rameau", "Couperin", "Boccherini"],
    "Classical": ["Mozart", "Haydn", "Clementi", "Beethoven", "Gluck", "Salieri", "Czerny"],
    "Romantic": ["Chopin", "Schubert", "Schumann", "Liszt", "Brahms", "Mendelssohn", "Grieg", "Dvorak",
                 "Dvořák", "Tchaikovsky", "Strauss", "Borodin", "Rimsky-Korsakov", "Mussorgsky", "Glazunov",
                 "Scriabin", "Rachmaninoff", "Rachmaninov", "Elgar", "Wagner", "Verdi", "Bizet", "Saint-Saëns",
                 "Saint-Saens", "Sibelius", "Paganini", "Rossini", "Offenbach", "Smetana", "Weber", "Berlioz",
                 "Bruckner", "Mahler", "Puccini", "Franck"],
    "Impressionist": ["Debussy", "Satie", "Ravel", "Fauré", "Faure"],
}
FULL = {
    "Bach": "Johann Sebastian Bach", "Handel": "George Frideric Handel", "Vivaldi": "Antonio Vivaldi",
    "Albinoni": "Tomaso Albinoni", "Telemann": "Georg Philipp Telemann", "Purcell": "Henry Purcell",
    "Pachelbel": "Johann Pachelbel", "Corelli": "Arcangelo Corelli", "Scarlatti": "Domenico Scarlatti",
    "Mozart": "Wolfgang Amadeus Mozart", "Haydn": "Joseph Haydn", "Clementi": "Muzio Clementi",
    "Beethoven": "Ludwig van Beethoven", "Chopin": "Frédéric Chopin", "Schubert": "Franz Schubert",
    "Schumann": "Robert Schumann", "Liszt": "Franz Liszt", "Brahms": "Johannes Brahms",
    "Mendelssohn": "Felix Mendelssohn", "Grieg": "Edvard Grieg", "Dvorak": "Antonín Dvořák",
    "Dvořák": "Antonín Dvořák", "Tchaikovsky": "Pyotr Ilyich Tchaikovsky", "Strauss": "Johann Strauss II",
    "Borodin": "Alexander Borodin", "Rimsky-Korsakov": "Nikolai Rimsky-Korsakov",
    "Mussorgsky": "Modest Mussorgsky", "Glazunov": "Alexander Glazunov", "Scriabin": "Alexander Scriabin",
    "Rachmaninoff": "Sergei Rachmaninoff", "Rachmaninov": "Sergei Rachmaninoff", "Debussy": "Claude Debussy",
    "Satie": "Erik Satie", "Ravel": "Maurice Ravel", "Fauré": "Gabriel Fauré", "Faure": "Gabriel Fauré",
    "Saint-Saens": "Camille Saint-Saëns", "Saint-Saëns": "Camille Saint-Saëns", "Elgar": "Edward Elgar",
    "Sibelius": "Jean Sibelius", "Bizet": "Georges Bizet", "Rossini": "Gioachino Rossini",
    "Wagner": "Richard Wagner", "Verdi": "Giuseppe Verdi", "Beethoven,": "Ludwig van Beethoven",
}


def era_of(artist):
    for era, names in ERA.items():
        if any(n in artist for n in names):
            return era
    return "Classical"


# Discovery sources: (Commons category, genre, max tracks, max per artist).
# Categories that do not exist / are rate limited are skipped gracefully.
EXTRA_SOURCES = [
    ("Category:Musopen", None, 45, 4),  # genre = composer era
    ("Category:Audio files of ragtime music", "Ragtime", 28, 2),
    ("Category:Audio files of jazz music", "Jazz", 28, 2),
    ("Category:Audio files of folk music", "Folk", 18, 2),
    ("Category:Audio files of marches", "March", 12, 2),
    ("Category:Audio files of blues music", "Blues", 12, 2),
    ("Category:Audio files of waltzes", "Waltz", 10, 2),
]
CORE_FILES = {m[0] for m in MANIFEST}
AUDIO_EXT = (".ogg", ".oga", ".opus", ".mp3", ".flac", ".wav")
MAX_BYTES = 30 << 20
MIN_SEC, MAX_SEC = 45, 480
QUOTED = re.compile(r'^"(?P<t>[^"]{2,80})"(?P<mid>[^"]*?)\b(?:performed by|sung by|played by|by)\s+(?P<a>.+)$', re.I)
YEAR = re.compile(r"\((1[89]\d\d|19\d\d)\)")

# gradient pairs from the TapeNest palette (#EEAA11 #4FB3B3 #BB3381 #3F1D50 #16141C)
COVERS = [("EEAA11", "BB3381"), ("4FB3B3", "3F1D50"), ("BB3381", "3F1D50"),
          ("EEAA11", "4FB3B3"), ("3F1D50", "16141C")]


def cover_filter(a, b):
    """Diagonal palette gradient with a stylised record (rings + label) in the middle."""
    ca = [int(a[i:i + 2], 16) for i in (0, 2, 4)]
    cb = [int(b[i:i + 2], 16) for i in (0, 2, 4)]
    exprs = []
    for c0, c1 in zip(ca, cb):
        grad = f"({c0}+({c1}-{c0})*(X+Y)/1200)"
        disc = f"({grad}*0.35)"
        ring = f"if(lt(mod(hypot(X-300,Y-300),14),1.5),{disc}+18,{disc})"
        label = f"({c0}*0.9)"
        exprs.append(
            f"if(lt(hypot(X-300,Y-300),12),{grad},if(lt(hypot(X-300,Y-300),70),{label},"
            f"if(lt(hypot(X-300,Y-300),210),{ring},{grad})))")
    return "format=rgb24,geq=r='{}':g='{}':b='{}'".format(*exprs)


def api_get(params):
    """Commons API GET with polite pacing and 429/5xx backoff."""
    q = urllib.parse.urlencode(params)
    req = urllib.request.Request(f"{API}?{q}", headers={"User-Agent": UA})
    for attempt in range(6):
        try:
            time.sleep(1.0)
            with urllib.request.urlopen(req, timeout=60) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            if e.code not in (429, 500, 502, 503, 504) or attempt == 5:
                raise
            time.sleep(10 * (attempt + 1))
    return {}


def _meta(ii):
    meta = ii.get("extmetadata", {})
    return {
        "url": ii["url"], "page": ii.get("descriptionurl", ""), "size": ii.get("size", 0),
        "mime": ii.get("mime", ""),
        "license": meta.get("LicenseShortName", {}).get("value", ""),
        "artist": strip_html(meta.get("Artist", {}).get("value", "")),
    }


def strip_html(v):
    v = html.unescape(re.sub(r"<[^>]+>", "", v or ""))
    return re.sub(r"\s+", " ", v).strip()


def api_info(titles):
    data = api_get({
        "action": "query", "format": "json", "prop": "imageinfo", "iiprop": "url|extmetadata|mime|size",
        "iiextmetadatafilter": "LicenseShortName|Artist|Credit", "titles": "|".join("File:" + t for t in titles),
    })
    norm = {n["to"]: n["from"] for n in data["query"].get("normalized", [])}
    out = {}
    for p in data["query"]["pages"].values():
        if "imageinfo" not in p:
            continue
        title = norm.get(p["title"], p["title"])[5:]
        out[title] = _meta(p["imageinfo"][0])
    return out


def category_files(cat, cap_scan=400):
    """Yields (file title without 'File:', meta) for audio files of a category."""
    cont = {}
    seen = 0
    while seen < cap_scan:
        try:
            data = api_get({
                "action": "query", "format": "json", "generator": "categorymembers", "gcmtitle": cat,
                "gcmtype": "file", "gcmlimit": "100", "prop": "imageinfo", "iiprop": "url|extmetadata|mime|size",
                "iiextmetadatafilter": "LicenseShortName|Artist", **cont,
            })
        except Exception as e:  # noqa: BLE001 - discovery is best effort
            print(f"discovery: {cat}: {type(e).__name__}", file=sys.stderr)
            return
        pages = sorted(data.get("query", {}).get("pages", {}).values(), key=lambda p: p["title"])
        for p in pages:
            seen += 1
            if "imageinfo" in p:
                yield p["title"][5:], _meta(p["imageinfo"][0])
        if "continue" not in data:
            return
        cont = {k: v for k, v in data["continue"].items()}


def norm_key(t):
    return re.sub(r"[^a-z0-9]", "", t.lower())[:40]


def fold(v):
    return "".join(c for c in unicodedata.normalize("NFKD", v) if not unicodedata.combining(c)).lower()


def parse_classical(stem):
    comp = None
    for short in sorted(FULL, key=len, reverse=True):
        if re.search(r"(?<![A-Za-z])" + re.escape(short.rstrip(",")) + r"(?![A-Za-z])", stem):
            comp = FULL[short]
            short_name = short.rstrip(",")
            break
    if not comp:
        return None
    t = re.sub(r"\([^()]*\bMusopen\b[^()]*\)", "", stem)  # "(Musopen Symphony)", "(Musopen version)"
    for junk in ("European Archive", "Musopen", "(Shelley Katz)"):
        t = t.replace(junk, "")
    t = re.sub(r"\(\s*" + re.escape(short_name) + r"\s*\)", "", t)
    for part in (comp, short_name + ", J S", short_name + ", Johann Sebastian", short_name):
        t = t.replace(part, "")
    parts = {fold(x) for x in comp.split()}
    words = t.split()
    while words and fold(words[0].strip(",.-")) in parts:
        words.pop(0)
    t = " ".join(words)
    t = re.sub(r"^[\s,.\-–]+|[\s,.\-–]+$", "", t)
    t = re.sub(r"\s{2,}", " ", t)
    t = tidy_parens(t)
    if len(t) < 3:
        return None
    return comp, t[:90]


def tidy_parens(t):
    """Drop empty "( )" groups and dangling separators inside parentheses ("(French, )" → "(French)")."""
    t = re.sub(r"\(\s*([^()]*?)[\s,;]*\)", lambda m: f"({m.group(1)})" if m.group(1).strip(" ,;") else "", t)
    return re.sub(r"\s{2,}", " ", t).strip()


def parse_quoted(stem, meta_artist):
    m = QUOTED.match(stem)
    year = YEAR.search(stem)
    if m:
        title, artist = m.group("t").strip(), m.group("a")
    else:
        title, artist = re.sub(r"\s*\(\d{4}\)", "", stem).strip(), meta_artist
    title = re.sub(r"\.(mscz|midi?|musicxml)$", "", title, flags=re.I)  # notation-export suffixes
    artist = YEAR.sub("", artist or "")
    if re.search(r"unknown|anonymous", artist, re.I):  # Commons "Unknown authorUnknown author"
        artist = "Traditional"
    artist = re.split(r"\s+(?:with|in|on|for|-|–)\s+|;\s|(?<=[a-z]{3})\.\s", artist)[0].strip(" ,.")
    if not artist or len(artist) > 48 or "http" in artist:
        artist = "Traditional" if not meta_artist or len(meta_artist) > 48 else meta_artist
    return tidy_parens(title)[:90], artist, (year.group(1) if year else "")


def discover(existing_keys, limit):
    picked = []
    for cat, genre, cap, per_artist in EXTRA_SOURCES:
        n, by_artist = 0, {}
        for title, meta in category_files(cat):
            if n >= cap or len(picked) >= limit:
                break
            low = title.lower()
            if not low.endswith(AUDIO_EXT) or meta["license"] not in ALLOWED:
                continue
            if meta["size"] <= 0 or meta["size"] > MAX_BYTES or not (meta["mime"].startswith("audio/")
                                                                    or meta["mime"] == "application/ogg"):
                continue
            stem = title.rsplit(".", 1)[0].replace("_", " ")
            if genre is None:
                parsed = parse_classical(stem)
                if not parsed or re.search(r"violin [A-Z] \d|viola [A-Z] \d|cello [A-Z] \d", stem, re.I):
                    continue
                artist, name = parsed
                g, year = era_of(artist), ""
            else:
                name, artist, year = parse_quoted(stem, meta["artist"])
                g = genre
            name = name[:1].upper() + name[1:]
            key = norm_key(name)
            if title in CORE_FILES or not key or key in existing_keys or by_artist.get(artist, 0) >= per_artist:
                continue
            existing_keys.add(key)
            by_artist[artist] = by_artist.get(artist, 0) + 1
            n += 1
            picked.append({"file": title, "meta": meta, "artist": artist, "name": name, "genre": g,
                           "year": year, "source": cat[9:]})
        print(f"discovery: {cat[9:]}: {n} candidates")
    return picked


def fetch(url, dst):
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    for attempt in range(5):
        try:
            with urllib.request.urlopen(req, timeout=300) as r, open(dst + ".part", "wb") as f:
                while chunk := r.read(1 << 16):
                    f.write(chunk)
            os.replace(dst + ".part", dst)
            time.sleep(1.0)
            return
        except urllib.error.HTTPError as e:
            if e.code not in (429, 500, 502, 503, 504) or attempt == 4:
                raise
            time.sleep(15 * (attempt + 1))


def duration(path):
    r = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path],
                       capture_output=True, text=True, check=False)
    try:
        return float(r.stdout.strip())
    except ValueError:
        return 0.0


def safe(name):
    return "".join(c if c.isalnum() or c in " -_.,'()" else "_" for c in name).strip()


def encode(src, dst, name, artist, album, album_artist, no, genre, comment, year="", bitrate="192k"):
    cmd = ["ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-i", src, "-vn", "-map_metadata", "-1",
           "-c:a", "libmp3lame", "-b:a", bitrate, "-ar", "44100", "-id3v2_version", "3",
           "-metadata", f"title={name}", "-metadata", f"artist={artist}", "-metadata", f"album={album}",
           "-metadata", f"album_artist={album_artist}", "-metadata", f"track={no}",
           "-metadata", f"genre={genre}", "-metadata", f"comment={comment}"]
    if year:
        cmd += ["-metadata", f"date={year}"]
    subprocess.run(cmd + [dst + ".tmp.mp3"], check=True)
    os.replace(dst + ".tmp.mp3", dst)


EXTRA_ALBUMS = {
    "Ragtime": "Ragtime Rarities", "Jazz": "Early Jazz Records", "Folk": "Folk & Traditional",
    "March": "Marches & Brass", "Blues": "Early Blues", "Waltz": "Waltz Time",
}


def main():
    args = [a for a in sys.argv[1:]]
    core_only = "--core-only" in args
    limit = 130
    if "--extra-limit" in args:
        limit = int(args[args.index("--extra-limit") + 1])
        del args[args.index("--extra-limit"):args.index("--extra-limit") + 2]
    args = [a for a in args if not a.startswith("--")]
    root = args[0] if args else os.environ.get("MUSIC_DIR", "data/music")
    cache = os.path.join(root, ".cache")
    os.makedirs(cache, exist_ok=True)
    info = {}
    for i in range(0, len(MANIFEST), 20):
        info.update(api_info([m[0] for m in MANIFEST[i:i + 20]]))
    credits = ["# Demo music credits", "",
               "Recordings from Wikimedia Commons (Musopen and public-domain early recordings), licensed as "
               "listed (only CC0 / Public domain accepted). Converted to MP3 by tools/dev/seed-music.py. "
               "Titles/artists of discovered files are parsed from Commons file names (best effort).", "",
               "| Track | Artist | Genre | License | Source |", "|---|---|---|---|---|"]
    albums = {}
    keys = set()
    for title, artist, album, name, no in MANIFEST:
        keys.add(norm_key(name))
        keys.add(norm_key(title.rsplit(".", 1)[0]))
        meta = info.get(title)
        if not meta:
            print(f"skip (not found on Commons): {title}", file=sys.stderr)
            continue
        if meta["license"] not in ALLOWED:
            print(f"skip (license {meta['license']!r} not allowed): {title}", file=sys.stderr)
            continue
        va = album == "Classical Miniatures"
        adir = os.path.join(root, safe("Various Artists" if va else artist), safe(album))
        os.makedirs(adir, exist_ok=True)
        albums.setdefault(adir, len(albums))
        genre = era_of(artist)
        dst = os.path.join(adir, f"{no:02d} - {safe(name)}.mp3")
        if not os.path.exists(dst):
            src = os.path.join(cache, safe(title))
            if not os.path.exists(src):
                print(f"download: {title}")
                fetch(meta["url"], src)
            encode(src, dst, name, artist, album, "Various Artists" if va else artist, no, genre,
                   f"Musopen via Wikimedia Commons, {meta['license']}")
        credits.append(f"| {name} | {artist} | {genre} | {meta['license']} | {meta['page']} |")
    extra = [] if core_only else discover(keys, limit)
    counters = {}
    skipped = 0
    for it in extra:
        g = it["genre"]
        if it["source"] == "Musopen":
            album, album_artist = f"{it['artist']} — Selected Works", it["artist"]
            adir = os.path.join(root, safe(it["artist"]), safe(album))
        else:
            album, album_artist = EXTRA_ALBUMS.get(g, f"{g} Collection"), "Various Artists"
            adir = os.path.join(root, "Various Artists", safe(album))
        counters[adir] = counters.get(adir, 0) + 1
        no = counters[adir]
        dst = os.path.join(adir, f"{no:02d} - {safe(it['name'])[:80]}.mp3")
        meta = it["meta"]
        if not os.path.exists(dst):
            src = os.path.join(cache, safe(it["file"]))
            try:
                if not os.path.exists(src):
                    print(f"download: {it['file']}")
                    fetch(meta["url"], src)
                d = duration(src)
                if not (MIN_SEC <= d <= MAX_SEC):
                    print(f"skip (duration {d:.0f}s): {it['file']}", file=sys.stderr)
                    counters[adir] -= 1
                    skipped += 1
                    continue
                os.makedirs(adir, exist_ok=True)
                encode(src, dst, it["name"], it["artist"], album, album_artist, no, g,
                       f"{it['source']} via Wikimedia Commons, {meta['license']}", it["year"], "128k")
            except (subprocess.CalledProcessError, OSError, urllib.error.URLError) as e:
                print(f"skip ({type(e).__name__}): {it['file']}", file=sys.stderr)
                counters[adir] -= 1
                skipped += 1
                continue
        albums.setdefault(adir, len(albums))
        credits.append(f"| {it['name']} | {it['artist']} | {g} | {meta['license']} | {meta['page']} |")
    for adir, idx in albums.items():
        cover = os.path.join(adir, "cover.jpg")
        if os.path.exists(cover):
            continue
        a, b = COVERS[idx % len(COVERS)]
        subprocess.run(["ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=black:s=600x600:d=1",
                        "-vf", cover_filter(a, b), "-frames:v", "1", "-q:v", "3", cover], check=True)
    with open(os.path.join(root, "CREDITS.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(credits) + "\n")
    n = sum(1 for d, _, fs in os.walk(root) if ".cache" not in d for x in fs if x.endswith(".mp3"))
    print(f"music dir ready: {n} tracks in {len(albums)} albums ({skipped} discovered files skipped)")


if __name__ == "__main__":
    main()
