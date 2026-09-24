#!/usr/bin/env python3
"""Build the "Axiell Datasets" Grafana dashboard.

Usage: build_dashboard.py <existing axiell.json> > axiell.json

The collection sections are generated here. Every other section of the
existing dashboard (the database backup and Azure copy panels, maintained in
the infrastructure repo) is carried over unchanged, below the generated ones.
"""
import copy
import json
import sys

DS = {"type": "prometheus", "uid": "prometheus"}
NEW = "#3987e5"       # blue: new collections
TRANSFER = "#d95926"  # orange: transfer collections
M = "axiell_collection_records"
F = 'institution=~"$institution", type=~"$type", collection=~"$collection"'

_next_id = iter(range(100, 1000))


def target(expr, legend="", ref="A", instant=False, fmt="time_series", interval=""):
    t = {"datasource": DS, "expr": expr, "legendFormat": legend, "refId": ref,
         "range": not instant, "instant": instant, "format": fmt}
    if interval:
        t["interval"] = interval
    return t


def panel(kind, title, x, y, w, h, targets, description="", **extra):
    p = {"id": next(_next_id), "type": kind, "title": title, "description": description,
         "datasource": DS, "gridPos": {"x": x, "y": y, "w": w, "h": h}, "targets": targets}
    p.update(extra)
    return p


def fixed(color):
    return {"mode": "fixed", "fixedColor": color}


def stat(title, x, y, w, h, expr, color, description, unit="locale", sparkline=None,
         mappings=None, thresholds=None, text_size=None):
    targets = [target(expr, ref="A", instant=sparkline is None)]
    if sparkline:
        targets = [target(expr, ref="A")]
    defaults = {"unit": unit, "decimals": 0, "noValue": "–", "mappings": mappings or [],
                "color": fixed(color) if color else {"mode": "thresholds"}}
    if thresholds:
        defaults["thresholds"] = {"mode": "absolute", "steps": thresholds}
    opts = {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
            "colorMode": "value", "graphMode": "area" if sparkline else "none",
            "justifyMode": "center", "textMode": "value", "orientation": "auto",
            "wideLayout": True, "showPercentChange": False}
    if text_size:
        opts["text"] = {"valueSize": text_size}
    return panel("stat", title, x, y, w, h, targets, description,
                 fieldConfig={"defaults": defaults, "overrides": []}, options=opts,
                 interval="1m", maxDataPoints=300)


def by_type_overrides():
    return [
        {"matcher": {"id": "byName", "options": "New"},
         "properties": [{"id": "color", "value": fixed(NEW)}]},
        {"matcher": {"id": "byName", "options": "Transfer"},
         "properties": [{"id": "color", "value": fixed(TRANSFER)}]},
    ]


def bargauge(title, x, y, w, h, typ, color, description):
    expr = f'sort_desc(max by (collection) ({M}{{type="{typ}", institution=~"$institution", collection=~"$collection"}}))'
    return panel(
        "bargauge", title, x, y, w, h, [target(expr, "{{collection}}", instant=True)], description,
        fieldConfig={"defaults": {"unit": "locale", "decimals": 0, "min": 0, "color": fixed(color),
                                  "thresholds": {"mode": "absolute", "steps": [{"color": color, "value": None}]}},
                     "overrides": []},
        options={"displayMode": "basic", "orientation": "horizontal", "valueMode": "text",
                 "namePlacement": "left", "showUnfilled": True, "sizing": "manual",
                 "minVizHeight": 18, "maxVizHeight": 22, "minVizWidth": 8,
                 "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                 "legend": {"showLegend": False}})


def timeseries(title, x, y, w, h, targets, description, stack=False, bars=False,
               legend_calcs=("lastNotNull",), legend_mode="list", legend_place="bottom",
               overrides=None, interval="1m"):
    custom = {"drawStyle": "bars" if bars else "line", "lineWidth": 2,
              "fillOpacity": 70 if bars else (25 if stack else 0), "gradientMode": "none",
              "showPoints": "never", "pointSize": 8, "spanNulls": True,
              "stacking": {"mode": "normal" if stack else "none", "group": "A"},
              "axisSoftMin": 0, "axisBorderShow": False, "barAlignment": 0,
              "lineInterpolation": "linear", "thresholdsStyle": {"mode": "off"}}
    return panel(
        "timeseries", title, x, y, w, h, targets, description, interval=interval,
        fieldConfig={"defaults": {"unit": "locale", "decimals": 0, "color": {"mode": "palette-classic"},
                                  "custom": custom},
                     "overrides": overrides or []},
        options={"legend": {"showLegend": True, "displayMode": legend_mode, "placement": legend_place,
                            "calcs": list(legend_calcs), "sortBy": "Last *", "sortDesc": True},
                 "tooltip": {"mode": "multi", "sort": "desc"}})


def added(sel):
    """Change in each series over the dashboard range. Series younger than the
    range have no value at the offset, so fall back to their earliest sample."""
    return f"({sel} - {sel} offset $__range) or ({sel} - min_over_time({sel}[$__range]))"


def row(title, y, collapsed=False, panels=None):
    return {"id": next(_next_id), "type": "row", "title": title, "collapsed": collapsed,
            "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": panels or []}


def collection_panels():
    ps = []
    y = 0
    ps.append(row("Collections at a glance", y)); y += 1

    # Headline: the two numbers everyone asks for, biggest on the page.
    ps.append(stat("New collections · records", 0, y, 9, 7, f'sum({M}{{type="new"}})', NEW,
                   "Object records in every collection flagged as new (new_collection = true on the "
                   "collname record). Ignores the filters above.", sparkline=True, text_size=64))
    ps.append(stat("Transfer collections · records", 9, y, 9, 7, f'sum({M}{{type="transfer"}})', TRANSFER,
                   "Object records in every collection not flagged as new. Ignores the filters above.",
                   sparkline=True, text_size=64))
    ps.append(stat("All object records", 18, y, 6, 4, "max(axiell_records)", "text",
                   "Total records in the objects database, whether or not they belong to a collection."))
    ps.append(stat("Records not in any collection", 18, y + 4, 6, 3,
                   f"clamp_min(max(axiell_records) - sum({M}), 0)", "text",
                   "Object records with no collection.name. Approximate: a record in two collections counts twice in the sum."))
    y += 7

    small = [
        ("New collections", f'count({M}{{type="new"}})', NEW, "Number of collections flagged as new."),
        ("Added to new · in range", f'sum({added(M + "{type=\"new\"}")})', NEW,
         "Records added to new collections over the selected time range."),
        ("Transfer collections", f'count({M}{{type="transfer"}})', TRANSFER, "Number of transfer collections."),
        ("Added to transfer · in range", f'sum({added(M + "{type=\"transfer\"}")})', TRANSFER,
         "Records added to transfer collections over the selected time range."),
    ]
    for i, (title, expr, color, desc) in enumerate(small):
        ps.append(stat(title, [0, 4, 9, 13][i], y, [4, 5, 4, 5][i], 3, expr, color, desc))
    ps.append(stat("Exporter", 18, y, 3, 3, "min(axiell_up)", None,
                   "Whether the last refresh from the Axiell API succeeded.",
                   mappings=[{"type": "value", "options": {"1": {"text": "OK", "color": "green", "index": 0},
                                                           "0": {"text": "Failing", "color": "red", "index": 1}}}],
                   thresholds=[{"color": "red", "value": None}, {"color": "green", "value": 1}]))
    ps.append(stat("Data age", 21, y, 3, 3, "time() - max(axiell_last_refresh_success_timestamp_seconds)", None,
                   "Time since the exporter last refreshed successfully from the API.", unit="s",
                   thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 900},
                               {"color": "red", "value": 3600}]))
    y += 3

    ps.append(row("Records per collection", y)); y += 1
    # Transfer has ~25 collections, so it gets the tall column; new has a
    # handful, so the type trend sits under it.
    ps.append(bargauge("New collections", 0, y, 8, 8, "new", NEW,
                       "Records in each new collection, largest first."))
    ps.append(bargauge("Transfer collections", 8, y, 16, 22, "transfer", TRANSFER,
                       "Records in each transfer collection, largest first. Empty bars are collections with no records yet."))
    ps.append(timeseries(
        "Records by type", 0, y + 8, 8, 14,
        [target(f'sum({M}{{type="new"}})', "New", "A"),
         target(f'sum({M}{{type="transfer"}})', "Transfer", "B")],
        "Total records in new and transfer collections over time.",
        overrides=by_type_overrides()))
    y += 22

    ps.append(row("Growth", y)); y += 1
    ps.append(timeseries(
        "Records added per day", 0, y, 8, 11,
        [target(f'sum({M}{{type="new"}} - {M}{{type="new"}} offset 1d)', "New", "A", interval="1d"),
         target(f'sum({M}{{type="transfer"}} - {M}{{type="transfer"}} offset 1d)', "Transfer", "B", interval="1d")],
        "Net records added each day, by collection type. Needs a day of history. "
        "Negative values mean records were deleted or moved.",
        bars=True, overrides=by_type_overrides(), legend_calcs=("sum",), interval="1d"))
    ps[-1]["fieldConfig"]["defaults"]["noValue"] = "Needs a day of history"
    ps.append(timeseries(
        "Records by collection", 8, y, 16, 11,
        [target(f"max by (collection) ({M}{{{F}}})", "{{collection}}", "A")],
        "Records in each collection over time. Use the filters above to narrow it down.",
        legend_mode="table", legend_place="right", legend_calcs=("lastNotNull", "delta")))
    y += 11

    ps.append(row("All collections", y)); y += 1
    sel = f"max by (collection, institution, type) ({M}{{{F}}})"
    ps.append(panel(
        "table", "Collections", 0, y, 24, 14,
        [target(sel, ref="A", instant=True, fmt="table"),
         target(f"100 * {sel} / scalar(sum({M}))", ref="B", instant=True, fmt="table"),
         target(f"max by (collection, institution, type) ({added(M + '{' + F + '}')})",
                ref="C", instant=True, fmt="table")],
        "Every collection with its type, record count, share of all collection records, and change over the selected range.",
        transformations=[
            {"id": "merge", "options": {}},
            {"id": "organize", "options": {
                "excludeByName": {"Time": True},
                "indexByName": {"collection": 0, "institution": 1, "type": 2,
                                "Value #A": 3, "Value #B": 4, "Value #C": 5},
                "renameByName": {"collection": "Collection", "institution": "Institution", "type": "Type",
                                 "Value #A": "Records", "Value #B": "Share", "Value #C": "Added in range"}}},
            {"id": "sortBy", "options": {"sort": [{"field": "Records", "desc": True}]}},
        ],
        fieldConfig={"defaults": {"custom": {"align": "auto", "cellOptions": {"type": "auto"}, "filterable": True},
                                  "noValue": "–"},
                     "overrides": [
                         {"matcher": {"id": "byName", "options": "Type"},
                          "properties": [{"id": "custom.width", "value": 110},
                                         {"id": "custom.cellOptions", "value": {"type": "color-text"}},
                                         {"id": "mappings", "value": [{"type": "value", "options": {
                                             "new": {"text": "New", "color": NEW, "index": 0},
                                             "transfer": {"text": "Transfer", "color": TRANSFER, "index": 1}}}]}]},
                         {"matcher": {"id": "byName", "options": "Records"},
                          "properties": [{"id": "unit", "value": "locale"}, {"id": "decimals", "value": 0},
                                         {"id": "custom.width", "value": 140}]},
                         {"matcher": {"id": "byName", "options": "Share"},
                          "properties": [{"id": "unit", "value": "percent"}, {"id": "decimals", "value": 1},
                                         {"id": "custom.width", "value": 110}]},
                         {"matcher": {"id": "byName", "options": "Added in range"},
                          "properties": [{"id": "unit", "value": "locale"}, {"id": "decimals", "value": 0},
                                         {"id": "custom.width", "value": 150}]},
                     ]},
        options={"showHeader": True, "cellHeight": "sm", "footer": {"show": False},
                 "sortBy": [{"displayName": "Records", "desc": True}]}))
    y += 14

    health = [
        panel("timeseries", "API refresh duration", 0, 0, 12, 7,
              [target("axiell_refresh_duration_seconds", "refresh")],
              "How long each refresh from the Axiell API takes.",
              fieldConfig={"defaults": {"unit": "s", "custom": {"lineWidth": 2, "showPoints": "never"}},
                           "overrides": []},
              options={"legend": {"showLegend": False}, "tooltip": {"mode": "single"}}),
        panel("timeseries", "API refresh failures", 12, 0, 12, 7,
              [target("increase(axiell_refresh_failures_total[$__rate_interval])", "failures")],
              "Failed refreshes. The dashboard keeps showing the last good numbers during failures.",
              fieldConfig={"defaults": {"unit": "short", "decimals": 0, "color": fixed("red"),
                                        "custom": {"drawStyle": "bars", "fillOpacity": 70}},
                           "overrides": []},
              options={"legend": {"showLegend": False}, "tooltip": {"mode": "single"}}),
    ]
    for p in health:
        p["gridPos"]["y"] = y + 1
    ps.append(row("Exporter health", y, collapsed=True, panels=health)); y += 1
    return ps, y


def variables():
    def q(name, label, query, include_all=True, multi=True):
        return {"name": name, "label": label, "type": "query", "datasource": DS,
                "definition": query, "query": {"query": query, "refId": f"{name}-var"},
                "refresh": 2, "includeAll": include_all, "multi": multi, "allValue": ".*",
                "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
                "options": [], "sort": 1, "hide": 0, "regex": "", "skipUrlSync": False}
    return [
        q("type", "Type", f"label_values({M}, type)"),
        q("institution", "Institution", f'label_values({M}{{type=~"$type"}}, institution)'),
        q("collection", "Collection",
          f'label_values({M}{{type=~"$type", institution=~"$institution"}}, collection)'),
    ]


def carried_over(old, generated):
    """Every panel of the existing dashboard that this script does not own:
    anything under a row whose title is not one of the generated rows. Those
    sections (the backup panels) are maintained by hand in the infrastructure
    repo and are kept exactly as they are."""
    owned_rows = {p["title"] for p in generated if p["type"] == "row"}
    keep, owned = [], False
    for p in old["panels"]:
        if p["type"] == "row":
            owned = p["title"] in owned_rows
        if not owned:
            keep.append(copy.deepcopy(p))
    return keep


def main():
    old = json.load(open(sys.argv[1]))
    panels, y = collection_panels()
    carried = carried_over(old, panels)

    # Shift the carried sections below the generated ones, keeping their layout.
    if carried:
        top = min(p["gridPos"]["y"] for p in carried)
        for p in carried:
            p["gridPos"]["y"] += y - top
            for nested in p.get("panels", []):
                nested["gridPos"]["y"] += y - top

    # Panel ids must be unique across the dashboard; renumber generated ones
    # that collide with a carried panel.
    taken = {q["id"] for p in carried for q in [p] + p.get("panels", [])}
    fresh = (i for i in range(max(taken | {0}) + 1, 10_000))
    for p in panels:
        for q in [p] + p.get("panels", []):
            if q["id"] in taken:
                q["id"] = next(fresh)
    panels += carried

    ours = variables()
    names = {v["name"] for v in ours}
    keep_vars = [v for v in old["templating"]["list"] if v["name"] not in names]
    dash = copy.deepcopy(old)
    dash.update({
        "panels": panels,
        "templating": {"list": ours + keep_vars},
        "refresh": "1m",
        "time": {"from": "now-30d", "to": "now"},
        "tags": sorted(set(old.get("tags", [])) | {"axiell", "collections"}),
        "graphTooltip": 1,
        "description": "Axiell Collections record counts per collection, split into new and transfer collections.",
        "version": old.get("version", 1) + 1,
    })
    json.dump(dash, sys.stdout, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
