package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

// expandMongoDatabases opens the Databases folder and selects the current
// database once its list arrives. It returns nil outside MongoDB mode.
func (m *Model) expandMongoDatabases() tea.Cmd {
	if !m.isMongo() || m.tree == nil {
		return nil
	}
	node := m.treeChild("Databases")
	if node == nil {
		return nil
	}
	node.expanded = true
	node.loading = true
	m.pendingCursor = m.currentDatabase()
	m.tree.rebuild()
	return m.loadNodeCmd(node)
}

// mongoDatabasesLoader lists all databases. Database nodes are expandable and
// switch the current database on Enter.
func mongoDatabasesLoader(cat engine.Catalog) func(context.Context) ([]*treeNode, error) {
	return func(ctx context.Context) ([]*treeNode, error) {
		items, err := cat.Databases(ctx)
		if err != nil {
			return nil, err
		}
		nodes := make([]*treeNode, 0, len(items))
		for _, item := range items {
			node := &treeNode{
				kind:    nodeDatabase,
				title:   item.Name,
				detail:  item.Detail,
				current: item.Current,
			}
			node.load = mongoDatabaseLoader(cat, item.Name)
			nodes = append(nodes, node)
		}
		return nodes, nil
	}
}

func mongoDatabaseLoader(cat engine.Catalog, database string) func(context.Context) ([]*treeNode, error) {
	return func(context.Context) ([]*treeNode, error) {
		return []*treeNode{
			{kind: nodeFolder, title: "Collections", schema: database, load: mongoCollectionsLoader(cat, database)},
			{kind: nodeFolder, title: "Views", schema: database, load: mongoViewsLoader(cat, database)},
		}, nil
	}
}

func mongoCollectionsLoader(cat engine.Catalog, database string) func(context.Context) ([]*treeNode, error) {
	return func(ctx context.Context) ([]*treeNode, error) {
		items, err := cat.Collections(ctx, database)
		if err != nil {
			return nil, err
		}
		return mongoCollectionNodes(cat, database, items), nil
	}
}

func mongoViewsLoader(cat engine.Catalog, database string) func(context.Context) ([]*treeNode, error) {
	return func(ctx context.Context) ([]*treeNode, error) {
		items, err := cat.Views(ctx, database)
		if err != nil {
			return nil, err
		}
		return mongoCollectionNodes(cat, database, items), nil
	}
}

func mongoCollectionNodes(cat engine.Catalog, database string, items []engine.Item) []*treeNode {
	nodes := make([]*treeNode, 0, len(items))
	for _, item := range items {
		node := &treeNode{
			kind:     nodeCollection,
			title:    item.Name,
			detail:   item.Detail,
			current:  item.Current,
			schema:   database,
			name:     item.Name,
			openable: true,
		}
		node.load = mongoIndexesLoader(cat, database, item.Name)
		nodes = append(nodes, node)
	}
	return nodes
}

func mongoIndexesLoader(cat engine.Catalog, database, collection string) func(context.Context) ([]*treeNode, error) {
	return func(ctx context.Context) ([]*treeNode, error) {
		items, err := cat.Indexes(ctx, database, collection)
		if err != nil {
			return nil, err
		}
		nodes := make([]*treeNode, 0, len(items))
		for _, item := range items {
			nodes = append(nodes, &treeNode{
				kind:   nodeIndex,
				title:  item.Name,
				detail: item.Detail,
				schema: database,
				name:   collection,
			})
		}
		return nodes, nil
	}
}

func mongoUsersLoader(cat engine.Catalog) func(context.Context) ([]*treeNode, error) {
	return func(ctx context.Context) ([]*treeNode, error) {
		items, err := cat.Users(ctx)
		if err != nil {
			return nil, err
		}
		nodes := itemNodes(items, nodeUser)
		// A user card is opened by database + username, so the node must carry
		// the user's home database.
		for i, node := range nodes {
			node.schema = items[i].Database
			node.name = items[i].Name
		}
		return nodes, nil
	}
}

func mongoRolesLoader(cat engine.Catalog) func(context.Context) ([]*treeNode, error) {
	return func(ctx context.Context) ([]*treeNode, error) {
		items, err := cat.Roles(ctx)
		if err != nil {
			return nil, err
		}
		return itemNodes(items, nodeMongoRole), nil
	}
}
