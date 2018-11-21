create table products (
    id text primary key,

    o_title text,
    o_desc text,
    o_price text,

    n_title text,
    n_desc text,
    n_price text,

    ready bool default false,
    likes int default 0
);

create table images (
    id text primary key,
    pid text references products(id),
    first bool default false
);
