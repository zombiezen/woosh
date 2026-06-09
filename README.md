# Woosh

Woosh is a CSS preprocessor that supports utility classes with optional suffixes
generated based on usage.

```css
@theme {
  --spacing: 0.25rem;
}

@utility p-* {
  padding: calc(var(--spacing) * --value(number));
  padding: --value([length]);
}
```

Then with an HTML file like:

```html
<div>
  <div class="p-2">...</div>
  <div class="p-[5px]">...</div>
</div>
```

Woosh will produce:

```css
:root {
  --spacing: 0.25rem;
}
.p-2 {
  padding: calc(var(--spacing) * 2);
}
.p-\[5px\] {
  padding: 5px;
}
```

## License

[MIT](LICENSE)
