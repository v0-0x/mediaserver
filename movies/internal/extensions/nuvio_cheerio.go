package extensions

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// A cheerio-compatible HTML API for Nuvio providers, which require
// "cheerio-without-node-native" (or "cheerio", "react-native-cheerio"):
//
//	const $ = cheerio.load(html)
//	$('a.item').each((i, el) => { const href = $(el).attr('href') })
//	const titles = $('h2').map((i, el) => $(el).text()).get()
//
// It is built on goquery, like LoadDoc, but keeps cheerio's semantics where
// scrapers rely on them: length is a number, selections are array-like
// (sel[0], for...of), callbacks get the element as `this` and as their
// second argument, elements expose attribs/tagName/type/children, and
// .map() results have .get() and .toArray().

// cheerioVM holds the prototypes shared by every document of a VM.
type cheerioVM struct {
	vm        *goja.Runtime
	key       *goja.Symbol // holds the *cheerioSel of a wrapper
	selProto  *goja.Object
	elemProto *goja.Object
	module    *goja.Object
}

// cheerioDoc is a loaded document; elements are wrapped once, so the same
// node is always the same JS object (=== works).
type cheerioDoc struct {
	cv    *cheerioVM
	doc   *goquery.Document
	elems map[*html.Node]*goja.Object
}

type cheerioSel struct {
	d *cheerioDoc
	s *goquery.Selection
}

// newCheerio builds the cheerio module object for vm.
func newCheerio(vm *goja.Runtime) *goja.Object {
	cv := &cheerioVM{vm: vm, key: goja.NewSymbol("cheerio")}
	cv.selProto = vm.NewObject()
	cv.elemProto = vm.NewObject()
	_ = cv.elemProto.SetPrototype(cv.selProto)
	cv.installSelectionMethods()
	cv.installElementProperties()

	m := vm.NewObject()
	_ = m.Set("load", func(call goja.FunctionCall) goja.Value {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(valueToString(vm, call.Argument(0))))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return cv.dollar(&cheerioDoc{cv: cv, doc: doc, elems: map[*html.Node]*goja.Object{}})
	})
	_ = m.Set("html", func(call goja.FunctionCall) goja.Value {
		if cs := cv.unwrap(call.Argument(0)); cs != nil {
			return vm.ToValue(cheerioOuterHTML(cs.s))
		}
		return vm.ToValue("")
	})
	_ = m.Set("text", func(call goja.FunctionCall) goja.Value {
		if cs := cv.unwrap(call.Argument(0)); cs != nil {
			return vm.ToValue(cs.s.Text())
		}
		return vm.ToValue("")
	})
	_ = m.Set("default", m)
	cv.module = m
	return m
}

// unwrap returns the selection behind a cheerio object or element, or nil.
func (cv *cheerioVM) unwrap(v goja.Value) *cheerioSel {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	h := o.GetSymbol(cv.key)
	if h == nil {
		return nil
	}
	cs, _ := h.Export().(*cheerioSel)
	return cs
}

// this returns the selection a method was called on.
func (cv *cheerioVM) this(call goja.FunctionCall) *cheerioSel {
	cs := cv.unwrap(call.This)
	if cs == nil {
		panic(cv.vm.NewTypeError("not a cheerio object"))
	}
	return cs
}

// sel builds a selection of nodes (in that order, without duplicates).
func (d *cheerioDoc) sel(nodes []*html.Node) *goquery.Selection {
	if len(nodes) == 0 {
		return d.doc.FindNodes()
	}
	return goquery.NewDocumentFromNode(nodes[0]).Selection.AddNodes(nodes[1:]...)
}

// wrap returns the cheerio object of a selection.
func (d *cheerioDoc) wrap(s *goquery.Selection) goja.Value {
	vm := d.cv.vm
	o := vm.NewObject()
	_ = o.SetPrototype(d.cv.selProto)
	_ = o.SetSymbol(d.cv.key, vm.ToValue(&cheerioSel{d: d, s: s}))
	_ = o.DefineDataProperty("length", vm.ToValue(len(s.Nodes)), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	for i, n := range s.Nodes {
		_ = o.Set(strconv.Itoa(i), d.elem(n))
	}
	return o
}

// elem returns the element object of a node (cheerio's raw node).
func (d *cheerioDoc) elem(n *html.Node) *goja.Object {
	if o, ok := d.elems[n]; ok {
		return o
	}
	vm := d.cv.vm
	o := vm.NewObject()
	_ = o.SetPrototype(d.cv.elemProto)
	_ = o.SetSymbol(d.cv.key, vm.ToValue(&cheerioSel{d: d, s: d.sel([]*html.Node{n})}))
	_ = o.DefineDataProperty("length", vm.ToValue(1), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	d.elems[n] = o
	return o
}

func (d *cheerioDoc) elemArray(s *goquery.Selection) goja.Value {
	out := make([]any, len(s.Nodes))
	for i, n := range s.Nodes {
		out[i] = d.elem(n)
	}
	return d.cv.vm.NewArray(out...)
}

// dollar is the $ function of a document.
func (cv *cheerioVM) dollar(d *cheerioDoc) goja.Value {
	vm := cv.vm
	fn := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		arg, ctx := call.Argument(0), call.Argument(1)
		if goja.IsUndefined(arg) || goja.IsNull(arg) {
			return d.wrap(d.sel(nil))
		}
		if cs := cv.unwrap(arg); cs != nil {
			return cs.d.wrap(cs.s)
		}
		if obj, ok := arg.(*goja.Object); ok && obj.ClassName() == "Array" {
			var nodes []*html.Node
			for _, k := range obj.Keys() {
				if cs := cv.unwrap(obj.Get(k)); cs != nil {
					nodes = append(nodes, cs.s.Nodes...)
				}
			}
			return d.wrap(d.sel(nodes))
		}
		str := strings.TrimSpace(arg.String())
		if str == "" {
			return d.wrap(d.sel(nil))
		}
		if strings.HasPrefix(str, "<") {
			return d.wrap(d.sel(cheerioParseFragment(str)))
		}
		if cs := cv.unwrap(ctx); cs != nil {
			return cs.d.wrap(cs.s.Find(str))
		}
		if !goja.IsUndefined(ctx) && !goja.IsNull(ctx) {
			return d.wrap(d.doc.Find(ctx.String()).Find(str))
		}
		return d.wrap(d.doc.Find(str))
	}).(*goja.Object)
	_ = fn.Set("html", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			h, _ := d.doc.Html()
			return vm.ToValue(h)
		}
		if cs := cv.unwrap(call.Argument(0)); cs != nil {
			return vm.ToValue(cheerioOuterHTML(cs.s))
		}
		return vm.ToValue(cheerioOuterHTML(d.doc.Find(call.Argument(0).String())))
	})
	_ = fn.Set("text", func(call goja.FunctionCall) goja.Value {
		if cs := cv.unwrap(call.Argument(0)); cs != nil {
			return vm.ToValue(cs.s.Text())
		}
		return vm.ToValue(d.doc.Text())
	})
	_ = fn.Set("root", func() goja.Value { return d.wrap(d.doc.Selection) })
	_ = fn.Set("contains", func(a, b goja.Value) bool {
		ca, cb := cv.unwrap(a), cv.unwrap(b)
		return ca != nil && cb != nil && len(cb.s.Nodes) > 0 && ca.s.Contains(cb.s.Nodes[0]) && ca.s.Nodes[0] != cb.s.Nodes[0]
	})
	_ = fn.Set("load", cv.module.Get("load"))
	return fn
}

func cheerioParseFragment(s string) []*html.Node {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), body)
	if err != nil {
		return nil
	}
	// Give the nodes a common parent so siblings work.
	for _, n := range nodes {
		body.AppendChild(n)
	}
	return nodes
}

func cheerioOuterHTML(s *goquery.Selection) string {
	var b strings.Builder
	for _, n := range s.Nodes {
		h, err := goquery.OuterHtml(goquery.NewDocumentFromNode(n).Selection)
		if err == nil {
			b.WriteString(h)
		}
	}
	return b.String()
}

// rawTextElements hold text that isn't HTML-escaped (scripts above all:
// scrapers read player configs out of them).
var rawTextElements = map[string]bool{
	"script": true, "style": true, "xmp": true, "iframe": true, "noembed": true, "noframes": true, "plaintext": true, "noscript": true,
}

// cheerioInnerHTML renders a node's children. goquery's Html() renders
// each child on its own, which escapes the text of a <script>.
func cheerioInnerHTML(n *html.Node) string {
	var b strings.Builder
	literal := n.Type == html.ElementNode && rawTextElements[strings.ToLower(n.Data)]
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if literal && c.Type == html.TextNode {
			b.WriteString(c.Data)
			continue
		}
		_ = html.Render(&b, c)
	}
	return b.String()
}

// cheerioOptString returns argument i as a string, false when absent.
func cheerioOptString(call goja.FunctionCall, i int) (string, bool) {
	a := call.Argument(i)
	if goja.IsUndefined(a) || goja.IsNull(a) {
		return "", false
	}
	return a.String(), true
}

func (cv *cheerioVM) installSelectionMethods() {
	vm, p := cv.vm, cv.selProto
	set := func(name string, fn func(cs *cheerioSel, call goja.FunctionCall) goja.Value) {
		_ = p.Set(name, func(call goja.FunctionCall) goja.Value { return fn(cv.this(call), call) })
	}
	// Traversal with an optional filter selector.
	type nav struct {
		name     string
		all      func(*goquery.Selection) *goquery.Selection
		filtered func(*goquery.Selection, string) *goquery.Selection
	}
	for _, n := range []nav{
		{"children", (*goquery.Selection).Children, (*goquery.Selection).ChildrenFiltered},
		{"parent", (*goquery.Selection).Parent, (*goquery.Selection).ParentFiltered},
		{"parents", (*goquery.Selection).Parents, (*goquery.Selection).ParentsFiltered},
		{"next", (*goquery.Selection).Next, (*goquery.Selection).NextFiltered},
		{"nextAll", (*goquery.Selection).NextAll, (*goquery.Selection).NextAllFiltered},
		{"prev", (*goquery.Selection).Prev, (*goquery.Selection).PrevFiltered},
		{"prevAll", (*goquery.Selection).PrevAll, (*goquery.Selection).PrevAllFiltered},
		{"siblings", (*goquery.Selection).Siblings, (*goquery.Selection).SiblingsFiltered},
	} {
		n := n
		set(n.name, func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
			if sel, ok := cheerioOptString(call, 0); ok {
				return cs.d.wrap(n.filtered(cs.s, sel))
			}
			return cs.d.wrap(n.all(cs.s))
		})
	}
	type until struct {
		name     string
		plain    func(*goquery.Selection, string) *goquery.Selection
		filtered func(*goquery.Selection, string, string) *goquery.Selection
	}
	for _, u := range []until{
		{"parentsUntil", (*goquery.Selection).ParentsUntil, (*goquery.Selection).ParentsFilteredUntil},
		{"nextUntil", (*goquery.Selection).NextUntil, (*goquery.Selection).NextFilteredUntil},
		{"prevUntil", (*goquery.Selection).PrevUntil, (*goquery.Selection).PrevFilteredUntil},
	} {
		u := u
		set(u.name, func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
			stop, _ := cheerioOptString(call, 0)
			if filter, ok := cheerioOptString(call, 1); ok {
				return cs.d.wrap(u.filtered(cs.s, filter, stop))
			}
			return cs.d.wrap(u.plain(cs.s, stop))
		})
	}
	set("find", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if other := cv.unwrap(call.Argument(0)); other != nil {
			return cs.d.wrap(cs.s.FindSelection(other.s))
		}
		return cs.d.wrap(cs.s.Find(call.Argument(0).String()))
	})
	set("contents", func(cs *cheerioSel, call goja.FunctionCall) goja.Value { return cs.d.wrap(cs.s.Contents()) })
	set("closest", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		sel, _ := cheerioOptString(call, 0)
		return cs.d.wrap(cs.s.Closest(sel))
	})
	set("first", func(cs *cheerioSel, call goja.FunctionCall) goja.Value { return cs.d.wrap(cs.s.First()) })
	set("last", func(cs *cheerioSel, call goja.FunctionCall) goja.Value { return cs.d.wrap(cs.s.Last()) })
	set("eq", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		return cs.d.wrap(cs.s.Eq(int(call.Argument(0).ToInteger())))
	})
	set("slice", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		n := len(cs.s.Nodes)
		start, end := cheerioClamp(int(call.Argument(0).ToInteger()), n), n
		if e, ok := cheerioOptString(call, 1); ok && e != "" {
			end = cheerioClamp(int(call.Argument(1).ToInteger()), n)
		}
		if start >= end {
			return cs.d.wrap(cs.d.sel(nil))
		}
		return cs.d.wrap(cs.d.sel(cs.s.Nodes[start:end]))
	})
	set("get", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) {
			return cs.d.elemArray(cs.s)
		}
		i := int(call.Argument(0).ToInteger())
		if i < 0 {
			i += len(cs.s.Nodes)
		}
		if i < 0 || i >= len(cs.s.Nodes) {
			return goja.Undefined()
		}
		return cs.d.elem(cs.s.Nodes[i])
	})
	set("toArray", func(cs *cheerioSel, call goja.FunctionCall) goja.Value { return cs.d.elemArray(cs.s) })
	set("index", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if other := cv.unwrap(call.Argument(0)); other != nil {
			return vm.ToValue(cs.s.IndexOfSelection(other.s))
		}
		if sel, ok := cheerioOptString(call, 0); ok {
			return vm.ToValue(cs.s.IndexSelector(sel))
		}
		return vm.ToValue(cs.s.Index())
	})
	predicate := func(cs *cheerioSel, v goja.Value) (string, func(int, *goquery.Selection) bool, *goquery.Selection) {
		if fn, ok := goja.AssertFunction(v); ok {
			return "", func(i int, x *goquery.Selection) bool {
				el := cs.d.elem(x.Nodes[0])
				res, err := fn(el, vm.ToValue(i), el)
				if err != nil {
					panic(err)
				}
				return res.ToBoolean()
			}, nil
		}
		if other := cv.unwrap(v); other != nil {
			return "", nil, other.s
		}
		return v.String(), nil, nil
	}
	set("filter", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		sel, fn, other := predicate(cs, call.Argument(0))
		switch {
		case fn != nil:
			return cs.d.wrap(cs.s.FilterFunction(fn))
		case other != nil:
			return cs.d.wrap(cs.s.FilterSelection(other))
		}
		return cs.d.wrap(cs.s.Filter(sel))
	})
	set("not", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		sel, fn, other := predicate(cs, call.Argument(0))
		switch {
		case fn != nil:
			return cs.d.wrap(cs.s.NotFunction(fn))
		case other != nil:
			return cs.d.wrap(cs.s.NotSelection(other))
		}
		return cs.d.wrap(cs.s.Not(sel))
	})
	set("is", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		sel, fn, other := predicate(cs, call.Argument(0))
		switch {
		case fn != nil:
			return vm.ToValue(cs.s.IsFunction(fn))
		case other != nil:
			return vm.ToValue(cs.s.IsSelection(other))
		}
		return vm.ToValue(cs.s.Is(sel))
	})
	set("has", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if other := cv.unwrap(call.Argument(0)); other != nil {
			return cs.d.wrap(cs.s.HasSelection(other.s))
		}
		return cs.d.wrap(cs.s.Has(call.Argument(0).String()))
	})
	set("add", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if other := cv.unwrap(call.Argument(0)); other != nil {
			return cs.d.wrap(cs.s.AddSelection(other.s))
		}
		return cs.d.wrap(cs.s.Union(cs.d.doc.Find(call.Argument(0).String())))
	})
	set("end", func(cs *cheerioSel, call goja.FunctionCall) goja.Value { return cs.d.wrap(cs.s.End()) })
	set("each", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(vm.NewTypeError("each() needs a function"))
		}
		for i, n := range cs.s.Nodes {
			el := cs.d.elem(n)
			res, err := fn(el, vm.ToValue(i), el)
			if err != nil {
				panic(err)
			}
			if b, isBool := res.Export().(bool); isBool && !b {
				break // returning false stops, as in jQuery
			}
		}
		return call.This
	})
	set("map", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(vm.NewTypeError("map() needs a function"))
		}
		var out []any
		for i, n := range cs.s.Nodes {
			el := cs.d.elem(n)
			res, err := fn(el, vm.ToValue(i), el)
			if err != nil {
				panic(err)
			}
			if goja.IsUndefined(res) || goja.IsNull(res) {
				continue
			}
			// Arrays are flattened one level, as in jQuery.
			if arr, isObj := res.(*goja.Object); isObj && arr.ClassName() == "Array" {
				for j := int64(0); j < intProp(arr, "length"); j++ {
					out = append(out, arr.Get(strconv.FormatInt(j, 10)))
				}
				continue
			}
			out = append(out, res)
		}
		return cheerioMapResult(vm, out)
	})
	set("text", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if v, ok := cheerioOptString(call, 0); ok {
			cs.s.SetText(v)
			return call.This
		}
		return vm.ToValue(cs.s.Text())
	})
	set("html", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if v, ok := cheerioOptString(call, 0); ok {
			cs.s.SetHtml(v)
			return call.This
		}
		if len(cs.s.Nodes) == 0 {
			return goja.Null()
		}
		return vm.ToValue(cheerioInnerHTML(cs.s.Nodes[0]))
	})
	set("toString", func(cs *cheerioSel, call goja.FunctionCall) goja.Value { return vm.ToValue(cheerioOuterHTML(cs.s)) })
	set("attr", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		a := call.Argument(0)
		if goja.IsUndefined(a) {
			if len(cs.s.Nodes) == 0 {
				return goja.Undefined()
			}
			return vm.ToValue(cheerioAttrs(cs.s.Nodes[0]))
		}
		if obj, isObj := a.(*goja.Object); isObj && cv.unwrap(a) == nil {
			for _, k := range obj.Keys() {
				cs.s.SetAttr(k, obj.Get(k).String())
			}
			return call.This
		}
		if v, ok := cheerioOptString(call, 1); ok {
			cs.s.SetAttr(a.String(), v)
			return call.This
		}
		if v, ok := cs.s.Attr(a.String()); ok {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})
	set("removeAttr", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		cs.s.RemoveAttr(call.Argument(0).String())
		return call.This
	})
	set("prop", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if len(cs.s.Nodes) == 0 {
			return goja.Undefined()
		}
		switch name := call.Argument(0).String(); name {
		case "innerHTML":
			return vm.ToValue(cheerioInnerHTML(cs.s.Nodes[0]))
		case "outerHTML":
			return vm.ToValue(cheerioOuterHTML(cs.s.First()))
		case "textContent", "innerText":
			return vm.ToValue(cs.s.First().Text())
		case "tagName", "nodeName":
			return vm.ToValue(strings.ToUpper(goquery.NodeName(cs.s.First())))
		case "checked", "selected", "disabled":
			_, ok := cs.s.Attr(name)
			return vm.ToValue(ok)
		default:
			if v, ok := cs.s.Attr(name); ok {
				return vm.ToValue(v)
			}
			return goja.Undefined()
		}
	})
	set("data", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if len(cs.s.Nodes) == 0 {
			return goja.Undefined()
		}
		if name, ok := cheerioOptString(call, 0); ok {
			if v, ok := cs.s.Attr("data-" + cheerioKebab(name)); ok {
				return parseJS(vm, json.RawMessage(cheerioDataValue(v)))
			}
			return goja.Undefined()
		}
		out := map[string]json.RawMessage{}
		for _, a := range cs.s.Nodes[0].Attr {
			if k, ok := strings.CutPrefix(a.Key, "data-"); ok {
				out[cheerioCamel(k)] = json.RawMessage(cheerioDataValue(a.Val))
			}
		}
		return toJS(vm, out)
	})
	set("val", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		first := cs.s.First()
		switch goquery.NodeName(first) {
		case "textarea":
			return vm.ToValue(first.Text())
		case "select":
			opt := first.Find("option[selected]").First()
			if opt.Length() == 0 {
				opt = first.Find("option").First()
			}
			if v, ok := opt.Attr("value"); ok {
				return vm.ToValue(v)
			}
			return vm.ToValue(opt.Text())
		}
		if v, ok := first.Attr("value"); ok {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})
	set("hasClass", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		return vm.ToValue(cs.s.HasClass(call.Argument(0).String()))
	})
	set("addClass", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		cs.s.AddClass(strings.Fields(call.Argument(0).String())...)
		return call.This
	})
	set("removeClass", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if c, ok := cheerioOptString(call, 0); ok {
			cs.s.RemoveClass(strings.Fields(c)...)
		} else {
			cs.s.RemoveClass()
		}
		return call.This
	})
	set("remove", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if sel, ok := cheerioOptString(call, 0); ok {
			cs.s.Filter(sel).Remove()
		} else {
			cs.s.Remove()
		}
		return call.This
	})
	set("empty", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		cs.s.Empty()
		return call.This
	})
	set("append", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if other := cv.unwrap(call.Argument(0)); other != nil {
			cs.s.AppendSelection(other.s)
		} else {
			cs.s.AppendHtml(call.Argument(0).String())
		}
		return call.This
	})
	set("prepend", func(cs *cheerioSel, call goja.FunctionCall) goja.Value {
		if other := cv.unwrap(call.Argument(0)); other != nil {
			cs.s.PrependSelection(other.s)
		} else {
			cs.s.PrependHtml(call.Argument(0).String())
		}
		return call.This
	})
	_ = p.Set("cheerio", "[cheerio object]")
	// for...of, spread and Array.from.
	if iter, err := vm.RunString(`(function(){ return Array.prototype[Symbol.iterator]; })()`); err == nil {
		_ = p.SetSymbol(goja.SymIterator, iter)
	}
}

// cheerioMapResult is what .map() returns: an array with cheerio's get() and
// toArray().
func cheerioMapResult(vm *goja.Runtime, items []any) goja.Value {
	arr := vm.NewArray(items...)
	_ = arr.Set("get", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) {
			return vm.NewArray(items...)
		}
		i := int(call.Argument(0).ToInteger())
		if i < 0 {
			i += len(items)
		}
		if i < 0 || i >= len(items) {
			return goja.Undefined()
		}
		return vm.ToValue(items[i])
	})
	_ = arr.Set("toArray", func() goja.Value { return vm.NewArray(items...) })
	return arr
}

// installElementProperties gives elements the properties of cheerio's raw
// nodes (domhandler): attribs, name/tagName, type, data, children...
func (cv *cheerioVM) installElementProperties() {
	vm, p := cv.vm, cv.elemProto
	node := func(call goja.FunctionCall) (*cheerioSel, *html.Node) {
		cs := cv.unwrap(call.This)
		if cs == nil || len(cs.s.Nodes) == 0 {
			return nil, nil
		}
		return cs, cs.s.Nodes[0]
	}
	getter := func(name string, fn func(cs *cheerioSel, n *html.Node) goja.Value) {
		_ = p.DefineAccessorProperty(name, vm.ToValue(func(call goja.FunctionCall) goja.Value {
			cs, n := node(call)
			if n == nil {
				return goja.Undefined()
			}
			return fn(cs, n)
		}), nil, goja.FLAG_TRUE, goja.FLAG_TRUE)
	}
	getter("attribs", func(cs *cheerioSel, n *html.Node) goja.Value { return vm.ToValue(cheerioAttrs(n)) })
	tag := func(cs *cheerioSel, n *html.Node) goja.Value {
		if n.Type != html.ElementNode {
			return goja.Undefined()
		}
		return vm.ToValue(strings.ToLower(n.Data))
	}
	getter("name", tag)
	getter("tagName", tag)
	getter("type", func(cs *cheerioSel, n *html.Node) goja.Value {
		switch n.Type {
		case html.ElementNode:
			switch strings.ToLower(n.Data) {
			case "script":
				return vm.ToValue("script")
			case "style":
				return vm.ToValue("style")
			}
			return vm.ToValue("tag")
		case html.TextNode:
			return vm.ToValue("text")
		case html.CommentNode:
			return vm.ToValue("comment")
		case html.DoctypeNode:
			return vm.ToValue("directive")
		case html.DocumentNode:
			return vm.ToValue("root")
		}
		return goja.Undefined()
	})
	getter("nodeType", func(cs *cheerioSel, n *html.Node) goja.Value {
		switch n.Type {
		case html.ElementNode:
			return vm.ToValue(1)
		case html.TextNode:
			return vm.ToValue(3)
		case html.CommentNode:
			return vm.ToValue(8)
		case html.DocumentNode:
			return vm.ToValue(9)
		}
		return vm.ToValue(0)
	})
	getter("data", func(cs *cheerioSel, n *html.Node) goja.Value {
		if n.Type == html.TextNode || n.Type == html.CommentNode {
			return vm.ToValue(n.Data)
		}
		return goja.Undefined()
	})
	getter("children", func(cs *cheerioSel, n *html.Node) goja.Value {
		var out []any
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			out = append(out, cs.d.elem(c))
		}
		return vm.NewArray(out...)
	})
	link := func(name string, next func(*html.Node) *html.Node) {
		getter(name, func(cs *cheerioSel, n *html.Node) goja.Value {
			if x := next(n); x != nil && x.Type != html.DocumentNode {
				return cs.d.elem(x)
			}
			return goja.Null()
		})
	}
	link("parentNode", func(n *html.Node) *html.Node { return n.Parent })
	link("firstChild", func(n *html.Node) *html.Node { return n.FirstChild })
	link("lastChild", func(n *html.Node) *html.Node { return n.LastChild })
	link("nextSibling", func(n *html.Node) *html.Node { return n.NextSibling })
	link("previousSibling", func(n *html.Node) *html.Node { return n.PrevSibling })
}

func cheerioAttrs(n *html.Node) map[string]string {
	out := map[string]string{}
	for _, a := range n.Attr {
		out[a.Key] = a.Val
	}
	return out
}

func cheerioClamp(i, n int) int {
	if i < 0 {
		i += n
	}
	return max(0, min(i, n))
}

var reCheerioDataNumber = regexp.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`)

// cheerioDataValue converts a data-* attribute the way jQuery does: JSON objects,
// arrays, numbers, booleans and null are parsed, the rest stays a string.
// It returns JSON.
func cheerioDataValue(v string) string {
	t := strings.TrimSpace(v)
	switch {
	case t == "true" || t == "false" || t == "null" || reCheerioDataNumber.MatchString(t):
		return t
	case (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t)):
		return t
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// cheerioKebab turns "fooBar" into "foo-bar" (data("fooBar") reads data-foo-bar).
func cheerioKebab(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsUpper(r) {
			b.WriteByte('-')
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cheerioCamel turns "foo-bar" into "fooBar".
func cheerioCamel(s string) string {
	parts := strings.Split(s, "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
