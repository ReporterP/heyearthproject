import 'package:flutter/material.dart';

class SearchPaginationBar extends StatelessWidget {
  const SearchPaginationBar({
    super.key,
    required this.searchController,
    required this.onSearch,
    required this.page,
    required this.totalPages,
    required this.total,
    required this.onPageChanged,
    this.searchHint = 'Search…',
    this.trailing,
  });

  final TextEditingController searchController;
  final VoidCallback onSearch;
  final int page;
  final int totalPages;
  final int total;
  final ValueChanged<int> onPageChanged;
  final String searchHint;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Row(
          children: [
            Expanded(
              child: TextField(
                controller: searchController,
                decoration: InputDecoration(
                  hintText: searchHint,
                  prefixIcon: const Icon(Icons.search),
                  suffixIcon: searchController.text.isNotEmpty
                      ? IconButton(
                          icon: const Icon(Icons.clear),
                          onPressed: () {
                            searchController.clear();
                            onSearch();
                          },
                        )
                      : null,
                  border: const OutlineInputBorder(),
                  isDense: true,
                  contentPadding:
                      const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                ),
                onSubmitted: (_) => onSearch(),
              ),
            ),
            const SizedBox(width: 8),
            FilledButton.icon(
              onPressed: onSearch,
              icon: const Icon(Icons.search, size: 18),
              label: const Text('Search'),
            ),
            if (trailing != null) ...[
              const SizedBox(width: 8),
              trailing!,
            ],
          ],
        ),
        const SizedBox(height: 8),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text(
              'Total: $total',
              style: Theme.of(context).textTheme.bodySmall,
            ),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                IconButton(
                  onPressed:
                      page > 1 ? () => onPageChanged(page - 1) : null,
                  icon: const Icon(Icons.chevron_left),
                  tooltip: 'Previous',
                ),
                Text(
                  '$page / $totalPages',
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
                IconButton(
                  onPressed: page < totalPages
                      ? () => onPageChanged(page + 1)
                      : null,
                  icon: const Icon(Icons.chevron_right),
                  tooltip: 'Next',
                ),
              ],
            ),
          ],
        ),
      ],
    );
  }
}
