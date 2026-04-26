"""Selector helpers for the SeleniumBase adapter.

Selenium has no native ``getByText`` equivalent, so
``SELECTOR_KIND_TEXT`` is mapped to an XPath ``contains(text(), …)``
expression. The naive single-quote variant fails the moment a
selector contains an apostrophe; the double-quote variant fails on
selectors containing a literal double quote. ``text_selector_to_xpath``
handles all three cases (no quotes, one quote type, both quote
types) by using XPath ``concat()`` to splice the two literals
together.

ADR-0015 §3 records the decision and the rejected alternatives.
"""

from __future__ import annotations


def text_selector_to_xpath(text: str) -> str:
    """Convert a TEXT-kind selector to a Selenium-safe XPath.

    Examples
    --------
    >>> text_selector_to_xpath("hello")
    "//*[contains(text(), 'hello')]"
    >>> text_selector_to_xpath("it's")
    '//*[contains(text(), "it\\'s")]'
    >>> text_selector_to_xpath('say "hi"')
    '//*[contains(text(), concat(\\'say "hi"\\'))]'
    """
    if "'" not in text:
        return f"//*[contains(text(), '{text}')]"
    if '"' not in text:
        return f'//*[contains(text(), "{text}")]'

    # Both quote types present. Split on double quotes so the
    # segments between them contain only single quotes (and other
    # safe characters); wrap each segment in single quotes when it
    # has no apostrophe and in double quotes when it does (after
    # the split the segment can never contain a literal "), then
    # splice the literal '"' between them via XPath concat().
    segments: list[str] = []
    for part in text.split('"'):
        if "'" not in part:
            segments.append(f"'{part}'")
        else:
            segments.append(f'"{part}"')
    expression = ", '\"', ".join(segments)
    return f"//*[contains(text(), concat({expression}))]"
